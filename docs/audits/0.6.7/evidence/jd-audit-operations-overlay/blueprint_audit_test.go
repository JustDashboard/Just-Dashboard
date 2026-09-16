package deploy
import("context";"testing";"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint")
func TestAuditBlueprintContract(t *testing.T){
 for _,id:=range []string{"minecraft-java","minecraft-bedrock","dozzle","postgresql"}{
  source:=DraftSourceConfig{Kind:SourceBlueprint,Mode:SourceModeBlueprint,BlueprintID:id,BlueprintVersion:"1.0.0",BlueprintInputs:map[string]string{}}
  if id=="minecraft-java"||id=="minecraft-bedrock"{source.BlueprintInputs["eula"]="true"}
  plan,err:=RenderBlueprintPlan(source,"audit");if err!=nil{t.Fatal(err)}
  t.Logf("%s normalized validation: %v",id,plan.Configuration.Validate())
  a:=NewHostSourceAnalyzer(nil,nil,"",nil,nil);_,err=a.Materialize(context.Background(),source,plan.Detection.Source,1,t.TempDir());t.Logf("%s materialization: %v",id,err)
  if id=="dozzle"{t.Logf("Dozzle mounts: %+v",plan.Configuration.Runtime.Mounts)}
 }
 a:=renderGamePlan(t,"same_name",map[string]string{"eula":"true"});b:=renderGamePlan(t,"same-name",map[string]string{"eula":"true"})
 t.Logf("distinct deployment names same_name/same-name volume=%q/%q",a.Configuration.Runtime.Mounts[0].Source,b.Configuration.Runtime.Mounts[0].Source)
 bp,_:=blueprint.Get("minecraft-bedrock");rendered,_:=blueprint.Render(bp,map[string]string{"eula":"true"});t.Logf("Bedrock schedules: %+v",BlueprintSchedules(rendered))
}
