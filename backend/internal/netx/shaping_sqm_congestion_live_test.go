package netx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

const sqmLoadServer = `
import socket, threading, time, sys
tcp=socket.socket(); tcp.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
tcp.bind((sys.argv[1],6001)); tcp.listen(8)
udp=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)
udp.setsockopt(socket.IPPROTO_IP,socket.IP_TOS,46<<2)
udp.bind((sys.argv[1],6002))
def echoes():
    while True:
        data,peer=udp.recvfrom(64); udp.sendto(data,peer)
def stream(conn,index):
    conn.setsockopt(socket.IPPROTO_IP,socket.IP_TOS,[0,8,46,34][index%4]<<2)
    conn.settimeout(2)
    try:
        while True: conn.sendall(b'x'*65536)
    except OSError: pass
    finally: conn.close()
threading.Thread(target=echoes,daemon=True).start()
open(sys.argv[2],'w').write('ready')
index=0
while True:
    conn,peer=tcp.accept()
    threading.Thread(target=stream,args=(conn,index),daemon=True).start(); index+=1
`

const sqmLoadReceiver = `
import socket, threading, time, sys, json, statistics, struct
peer=sys.argv[1]; counts=[0]*4; stop=threading.Event()
def download(index):
    conn=socket.socket(); conn.settimeout(.2)
    conn.setsockopt(socket.SOL_SOCKET,socket.SO_RCVBUF,4*1024*1024)
    conn.connect((peer,6001))
    try:
        while not stop.is_set():
            try: data=conn.recv(65536)
            except socket.timeout: continue
            if not data: break
            counts[index]+=len(data)
    finally: conn.close()
threads=[threading.Thread(target=download,args=(i,),daemon=True) for i in range(4)]
for thread in threads: thread.start()
udp=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); udp.settimeout(.3)
udp.setsockopt(socket.IPPROTO_IP,socket.IP_RECVTOS,1)
time.sleep(2); first=list(counts); start=time.monotonic(); rtts=[]; markings=set(); sent=0
while time.monotonic()-start<5:
    nonce=struct.pack('!I',sent); sent+=1; before=time.monotonic()
    udp.sendto(nonce,(peer,6002))
    try:
        while True:
            data,anc,flags,addr=udp.recvmsg(64,64)
            if data==nonce: break
        rtts.append((time.monotonic()-before)*1000)
        for level,kind,data in anc:
            if level==socket.IPPROTO_IP and kind==socket.IP_TOS: markings.add(data[0]>>2)
    except socket.timeout: pass
    time.sleep(max(0,.02-(time.monotonic()-before)))
elapsed=time.monotonic()-start; last=list(counts); stop.set(); udp.close()
for thread in threads: thread.join(1)
rtts.sort(); measured=[b-a for a,b in zip(first,last)]
print(json.dumps({'durationSeconds':elapsed,'tcpFlows':4,'tcpBytes':measured,
'throughputMbit':sum(measured)*8/elapsed/1e6,'udpSent':sent,'udpReplies':len(rtts),
'medianMillis':statistics.median(rtts) if rtts else None,
'p95Millis':rtts[min(len(rtts)-1,int(len(rtts)*.95))] if rtts else None,
'receivedDscp':sorted(markings)}))
`

type sqmLoadReading struct {
	DurationSeconds float64  `json:"durationSeconds"`
	TCPFlows        int      `json:"tcpFlows"`
	TCPBytes        []uint64 `json:"tcpBytes"`
	ThroughputMbit  float64  `json:"throughputMbit"`
	UDPSent         int      `json:"udpSent"`
	UDPReplies      int      `json:"udpReplies"`
	MedianMillis    float64  `json:"medianMillis"`
	P95Millis       float64  `json:"p95Millis"`
	ReceivedDSCP    []int    `json:"receivedDscp"`
}

func TestLiveSQMRecordsCongestionLatencyAndDSCPUnderDeclaredLoad(t *testing.T) {
	gwLiveRequired(t)
	receiver, sender := gwLiveNS(t, "sqm-rx"), gwLiveNS(t, "sqm-tx")
	gwMustInNS(t, receiver, "ip", "link", "add", "wire0", "type", "veth", "peer", "name", "wire1")
	gwMustInNS(t, receiver, "ip", "link", "set", "wire1", "netns", sender)
	gwMustInNS(t, receiver, "ip", "link", "set", "wire0", "up")
	gwMustInNS(t, receiver, "ip", "link", "add", "link", "wire0", "name", "wan0", "type", "macvlan", "mode", "bridge")
	gwMustInNS(t, receiver, "ip", "link", "set", "wan0", "up")
	gwMustInNS(t, receiver, "ip", "addr", "add", "192.0.2.1/30", "dev", "wan0")
	gwMustInNS(t, sender, "ip", "link", "set", "wire1", "up")
	gwMustInNS(t, sender, "ip", "addr", "add", "192.0.2.2/30", "dev", "wire1")
	// A deliberately declared access bottleneck, not a model of every NIC
	// or provider. Both readings use the same 10 Mbit/s FIFO path and load.
	gwMustInNS(t, sender, "tc", "qdisc", "add", "dev", "wire1", "root", "handle", "1:", "htb", "default", "10")
	gwMustInNS(t, sender, "tc", "class", "add", "dev", "wire1", "parent", "1:", "classid", "1:10", "htb", "rate", "10000kbit", "ceil", "10000kbit")
	gwMustInNS(t, sender, "tc", "qdisc", "add", "dev", "wire1", "parent", "1:10", "handle", "10:", "bfifo", "limit", "256000")
	ready := filepath.Join(t.TempDir(), "server-ready")
	server := gwLiveCmd(context.Background(), "ip", "netns", "exec", sender, "python3", "-c", sqmLoadServer, "192.0.2.2", ready)
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	server.Stdout, server.Stderr = &output, &output
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = gwLiveCmd(context.Background(), "kill", "-KILL", "--", fmt.Sprintf("-%d", server.Process.Pid)).Run()
		_ = server.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("load server did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	previous, previousStdin := run, runStdin
	run = gwLiveRun(receiver)
	runStdin = func(ctx context.Context, input []byte, name string, args ...string) (string, error) {
		if len(input) != 0 {
			t.Fatal("unexpected stdin")
		}
		return run(ctx, name, args...)
	}
	t.Cleanup(func() { run, runStdin = previous, previousStdin })
	ctx := context.Background()
	measure := func() sqmLoadReading {
		out := gwMustInNS(t, receiver, "python3", "-c", sqmLoadReceiver, "192.0.2.2")
		var reading sqmLoadReading
		if err := json.Unmarshal([]byte(out), &reading); err != nil {
			t.Fatalf("load output: %s %v", out, err)
		}
		if reading.DurationSeconds < 5 || reading.TCPFlows != 4 || len(reading.TCPBytes) != 4 || reading.UDPReplies < 10 || slices.Contains(reading.TCPBytes, uint64(0)) {
			t.Fatalf("insufficient declared load or latency samples: %+v", reading)
		}
		return reading
	}
	legacy := ShapeSpec{Device: "wan0", IngressKbit: 10000}
	if err := runShapeLines(ctx, shapeLines(legacy)); err != nil {
		t.Fatal(err)
	}
	policing := measure()
	removeIngress(ctx, "wan0")
	gwMustInNS(t, receiver, "tc", "qdisc", "add", "dev", "wan0", "clsact")
	gwMustInNS(t, receiver, "tc", "filter", "add", "dev", "wan0", "egress", "protocol", "all", "pref", "7", "matchall", "action", "pass")
	egressBefore := gwMustInNS(t, receiver, "tc", "-j", "filter", "show", "dev", "wan0", "egress")
	sh := liveSQMShape(t, ctx, "wan0")
	if err := applySQM(ctx, sh, nil); err != nil {
		t.Fatal(err)
	}
	sqm := measure()
	if !slices.Contains(policing.ReceivedDSCP, 46) || !slices.Equal(sqm.ReceivedDSCP, []int{0}) {
		t.Fatalf("native post-classification DSCP wash was not observed: legacy=%v SQM=%v", policing.ReceivedDSCP, sqm.ReceivedDSCP)
	}
	stats := gwMustInNS(t, receiver, "tc", "-j", "-s", "qdisc", "show", "dev", sh.SQM.IFB)
	queues, err := parseQdiscs(stats)
	if err != nil || len(queues) != 1 || queues[0].Packets == 0 {
		t.Fatalf("declared load did not traverse IFB: %s %v", stats, err)
	}
	if got := gwMustInNS(t, receiver, "tc", "-j", "filter", "show", "dev", "wan0", "egress"); got != egressBefore {
		t.Fatal("foreign egress changed during real download load")
	}
	readings, _ := json.Marshal(map[string]any{"scope": "two disposable namespaces; 10 Mbit/s HTB + 256000-byte FIFO bottleneck; four saturated TCP downloads marked DSCP 0/8/46/34 plus 46-marked UDP echo; 2s warmup + 5s samples per mode; no BBR change", "legacyPolicerKbit": 10000, "sqmKbit": 9000, "legacy": policing, "sqm": sqm, "sqmQueue": queues[0]})
	t.Logf("Congestion observations (local scope; no universal performance assertion): %s", readings)
}
