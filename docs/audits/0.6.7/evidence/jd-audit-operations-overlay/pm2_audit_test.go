package procs
import("context";"fmt";"os";"path/filepath";"strings";"testing";"time")
func TestAuditPM2ExecutesDiscoveredUserBinary(t *testing.T){
 home:=t.TempDir(); bin:=filepath.Join(home,".local","bin","pm2")
 if err:=os.MkdirAll(filepath.Dir(bin),0700);err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(bin,[]byte("#!/bin/sh\nid -u\n"),0700);err!=nil{t.Fatal(err)}
 homes:=discoverPM2HomesIn([]string{home}); if len(homes)!=1{t.Fatalf("homes=%v",homes)}
 res,err:=runPM2Host(context.Background(),homes[0],time.Second,"jlist"); if err!=nil{t.Fatal(err)}
 if strings.TrimSpace(res.Stdout)!=fmt.Sprint(os.Geteuid()){t.Fatal(res.Stdout)}
 t.Logf("Discovered user-writable binary executed with parent uid=%d, output=%q; shipped parent runs as root",os.Geteuid(),strings.TrimSpace(res.Stdout))
}
