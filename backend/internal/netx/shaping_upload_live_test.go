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

const uploadSink = `
import socket, threading, sys
tcp=socket.socket(); tcp.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
tcp.bind((sys.argv[1],6001)); tcp.listen(8)
udp=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); udp.bind((sys.argv[1],6002))
def echoes():
    while True:
        data,peer=udp.recvfrom(64); udp.sendto(data,peer)
def drain(conn):
    try:
        while conn.recv(65536): pass
    except OSError: pass
    finally: conn.close()
threading.Thread(target=echoes,daemon=True).start()
open(sys.argv[2],'w').write('ready')
while True:
    conn,peer=tcp.accept()
    threading.Thread(target=drain,args=(conn,),daemon=True).start()
`

const uploadSender = `
import socket, threading, time, sys, json, statistics, struct
peer=sys.argv[1]; counts=[0]*4; stop=threading.Event()
def upload(index):
    conn=socket.socket(); conn.settimeout(.2)
    conn.setsockopt(socket.SOL_SOCKET,socket.SO_SNDBUF,4*1024*1024)
    conn.connect((peer,6001)); chunk=b'x'*65536
    try:
        while not stop.is_set():
            try: counts[index]+=conn.send(chunk)
            except socket.timeout: continue
    except OSError: pass
    finally: conn.close()
threads=[threading.Thread(target=upload,args=(i,),daemon=True) for i in range(4)]
for thread in threads: thread.start()
udp=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); udp.settimeout(.5)
time.sleep(2); first=list(counts); start=time.monotonic(); rtts=[]; sent=0
while time.monotonic()-start<5:
    nonce=struct.pack('!I',sent); sent+=1; before=time.monotonic()
    udp.sendto(nonce,(peer,6002))
    try:
        while True:
            data,addr=udp.recvfrom(64)
            if data==nonce: break
        rtts.append((time.monotonic()-before)*1000)
    except socket.timeout: pass
    time.sleep(max(0,.02-(time.monotonic()-before)))
elapsed=time.monotonic()-start; last=list(counts); stop.set(); udp.close()
for thread in threads: thread.join(1)
rtts.sort(); measured=[b-a for a,b in zip(first,last)]
print(json.dumps({'durationSeconds':elapsed,'tcpFlows':4,'tcpBytes':measured,
'throughputMbit':sum(measured)*8/elapsed/1e6,'udpSent':sent,'udpReplies':len(rtts),
'medianMillis':statistics.median(rtts) if rtts else None,
'p95Millis':rtts[min(len(rtts)-1,int(len(rtts)*.95))] if rtts else None}))
`

// The latency an upload profile buys, measured rather than asserted from the
// configuration: four saturating uploads through a declared 10 Mbit/s access
// link with a deep FIFO, once with the kernel's own queue in front of it and
// once with the CAKE upload profile at 9 Mbit/s, the round trip of a small
// echo measured through the same path under the same load. The profile is
// applied with the same lines the boot unit renders and must verify exactly
// against the kernel before it is measured.
func TestLiveUploadProfileMeasuresQueueDelayUnderDeclaredLoad(t *testing.T) {
	gwLiveRequired(t)
	sender, receiver := gwLiveNS(t, "upl-tx"), gwLiveNS(t, "upl-rx")
	gwMustInNS(t, sender, "ip", "link", "add", "wire0", "type", "veth", "peer", "name", "wire1")
	gwMustInNS(t, sender, "ip", "link", "set", "wire1", "netns", receiver)
	gwMustInNS(t, sender, "ip", "link", "set", "wire0", "up")
	gwMustInNS(t, sender, "ip", "link", "add", "link", "wire0", "name", "wan0", "type", "macvlan", "mode", "bridge")
	gwMustInNS(t, sender, "ip", "link", "set", "wan0", "up")
	gwMustInNS(t, sender, "ip", "addr", "add", "192.0.2.1/30", "dev", "wan0")
	gwMustInNS(t, receiver, "ip", "link", "set", "wire1", "up")
	gwMustInNS(t, receiver, "ip", "addr", "add", "192.0.2.2/30", "dev", "wire1")
	// The declared access link: 10 Mbit/s with a 256000-byte FIFO behind the
	// shaped device, as a modem's buffer is. Both readings cross it.
	gwMustInNS(t, sender, "tc", "qdisc", "add", "dev", "wire0", "root", "handle", "1:", "htb", "default", "10")
	gwMustInNS(t, sender, "tc", "class", "add", "dev", "wire0", "parent", "1:", "classid", "1:10", "htb", "rate", "10000kbit", "ceil", "10000kbit")
	gwMustInNS(t, sender, "tc", "qdisc", "add", "dev", "wire0", "parent", "1:10", "handle", "10:", "bfifo", "limit", "256000")

	ready := filepath.Join(t.TempDir(), "sink-ready")
	sink := gwLiveCmd(context.Background(), "ip", "netns", "exec", receiver, "python3", "-c", uploadSink, "192.0.2.2", ready)
	sink.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var output bytes.Buffer
	sink.Stdout, sink.Stderr = &output, &output
	if err := sink.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = gwLiveCmd(context.Background(), "kill", "-KILL", "--", fmt.Sprintf("-%d", sink.Process.Pid)).Run()
		_ = sink.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("upload sink did not become ready: %s", output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	previous := run
	run = gwLiveRun(sender)
	t.Cleanup(func() { run = previous })
	ctx := context.Background()
	measure := func() sqmLoadReading {
		out := gwMustInNS(t, sender, "python3", "-c", uploadSender, "192.0.2.2")
		var reading sqmLoadReading
		if err := json.Unmarshal([]byte(out), &reading); err != nil {
			t.Fatalf("load output: %s %v", out, err)
		}
		if reading.DurationSeconds < 5 || reading.TCPFlows != 4 || reading.UDPReplies < 10 || slices.Contains(reading.TCPBytes, uint64(0)) {
			t.Fatalf("insufficient declared load or latency samples: %+v", reading)
		}
		return reading
	}

	fifo := measure()
	sh, err := normShape(ShapeSpec{Device: "wan0", Qdisc: "cake", EgressKbit: 9000,
		Upload: &UploadProfile{Diffserv: "diffserv4", Overhead: 18, MPU: 64, RTTMillis: 50}})
	if err != nil {
		t.Fatal(err)
	}
	if err := runShapeLines(ctx, shapeLines(sh)); err != nil {
		t.Fatal(err)
	}
	if err := verifyShaping(ctx, sh); err != nil {
		t.Fatalf("the kernel's CAKE differs from the rendered profile: %v", err)
	}
	cake := measure()
	stats := gwMustInNS(t, sender, "tc", "-j", "-s", "qdisc", "show", "dev", "wan0")
	queues, err := parseQdiscs(stats)
	if err != nil {
		t.Fatal(err)
	}
	var tins []CakeTin
	for _, q := range queues {
		if q.Root {
			tins = qdiscStat(q).Tins
		}
	}
	if len(tins) != 4 || tins[1].SentPackets == 0 || tins[1].PeakDelayUs == 0 {
		t.Fatalf("CAKE measured nothing under load: %s", stats)
	}
	if !(cake.MedianMillis < fifo.MedianMillis) {
		t.Fatalf("the upload profile did not lower the loaded round trip: fifo %+v cake %+v", fifo, cake)
	}
	readings, _ := json.Marshal(map[string]any{"scope": "two disposable namespaces; macvlan wan0 over veth wire0 with a 10 Mbit/s HTB + 256000-byte FIFO access link; four saturated TCP uploads plus a 20 ms UDP echo; 2s warmup + 5s samples per mode; no BBR change",
		"fifo": fifo, "cakeKbit": 9000, "cake": cake, "cakeTins": tins})
	t.Logf("Upload latency observations (local scope; no universal performance assertion beyond this fixture): %s", readings)
	// Clearing the profile hands the device back to its kernel default.
	removeRoot(ctx, "wan0")
	qs, err := deviceQdiscs(ctx, "wan0")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range qs {
		if q.Root && q.Kind == "cake" {
			t.Fatalf("CAKE remained after clearing: %+v", qs)
		}
	}
}
