package proxysvc
import("os";"path/filepath";"testing")
func TestAuditLinkRollbackLosesOriginal(t *testing.T){
 d:=t.TempDir();link:=filepath.Join(d,"enabled"); old:=filepath.Join(d,"old");new:=filepath.Join(d,"new")
 if err:=os.Symlink(old,link);err!=nil{t.Fatal(err)}
 undo,err:=linkEnabled(link,new);if err!=nil{t.Fatal(err)};undo()
 _,err=os.Readlink(link);t.Logf("previous symlink after rollback: %v",err);if !os.IsNotExist(err){t.Fatal("unexpected restored state")}
}
