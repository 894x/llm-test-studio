import subprocess, tarfile, io, json, sqlite3, os, base64, uuid
from pathlib import Path

root=Path.cwd().resolve()
catalog=json.loads((root/'internal/persistence/sqlite/migrations/operational_baselines.json').read_text())
out=root/'internal/persistence/sqlite/testdata/migrations'
out.mkdir(parents=True,exist_ok=True)
test=r'''package sqlite_test
import (
 "os"
 "testing"
 "time"
 "github.com/894x/llm-test-studio/internal/domain"
 persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)
func TestExportHistoricalMigrationFixture(t *testing.T) {
 path:=os.Getenv("MIGRATION_FIXTURE_DB")
 if err:=persistence.Migrate(t.Context(),path,persistence.MigrateOptions{AppVersion:"historical-fixture"});err!=nil {t.Fatal(err)}
 repository:=reopenHardeningRepository(t,path)
 defer repository.Close()
 spec:=jobSpecForRun(t,runWithCompleteSnapshot(t))
 if _,_,err:=repository.AcceptJob(t.Context(),jobAcceptance(spec));err!=nil {t.Fatal(err)}
 fixture:=newRepositoryFixture(t)
 run:=createRunningOutputs(t,repository,fixture)
 run=transitionRun(t,repository,run,domain.RunCompleted)
 report:=fixture.report
 report.RunStatus=run.Status()
 if err:=repository.CreateReport(t.Context(),report);err!=nil {t.Fatal(err)}
 quick:=validQuickPerformanceReport("77777777-7777-4777-8777-777777777771",repositoryEpoch.Add(time.Hour).Format(time.RFC3339Nano))
 if err:=repository.SaveQuickPerformanceReport(t.Context(),quick);err!=nil {t.Fatal(err)}
}
'''
baselines = catalog['baselines']
if os.environ.get('MIGRATION_SOURCE_COMMIT'):
 baselines = [{'commit': subprocess.check_output(['git','rev-parse',os.environ['MIGRATION_SOURCE_COMMIT']],text=True).strip()}]
for baseline in baselines:
 ref=baseline['commit']; short=ref[:8]
 destination=out/(short+'.json')
 if destination.exists() and not os.environ.get("REBUILD_FIXTURES"): continue
 checkout=root/'.tmp/migration-producers'/short
 checkout.mkdir(parents=True,exist_ok=True)
 archive=subprocess.check_output(['git','archive',ref])
 with tarfile.open(fileobj=io.BytesIO(archive)) as tf:
  tf.extractall(checkout,filter='data')
 producer=test.replace('if _,_,err:=repository.AcceptJob(t.Context(),jobAcceptance(spec));err!=nil {t.Fatal(err)}', 'snapshot,_,err:=repository.AcceptJob(t.Context(),jobAcceptance(spec));if err!=nil {t.Fatal(err)}; _ = snapshot')
 extra=''
 if (checkout/'internal/persistence/sqlite/repository_submissions_test.go').exists():
  producer=producer.replace('"os"','"os"\n "github.com/894x/llm-test-studio/internal/application/jobs"')
  extra += '''record:=submissionFixture(t,spec)
 if _,err:=repository.CreateSubmission(t.Context(),record);err!=nil {t.Fatal(err)}
 record=pendingSubmission(record)
 if err:=repository.UpdateSubmission(t.Context(),record,record.Revision-1);err!=nil {t.Fatal(err)}
 record.Revision++; record.State=jobs.SubmissionAccepted; record.Receipt=&snapshot.Receipt; record.UpdatedAt=snapshot.UpdatedAt.Add(time.Second)
 if err:=repository.UpdateSubmission(t.Context(),record,record.Revision-1);err!=nil {t.Fatal(err)}
 '''
 if (checkout/'internal/persistence/sqlite/repository_control_outbox_test.go').exists():
  signature=(checkout/'internal/persistence/sqlite/repository_control_outbox_test.go').read_text()
  control='controlFixture(t,repository,spec.JobID,1)' if 'func controlFixture(t ' in signature else 'controlFixture(spec.JobID,1)'
  extra += 'if _,err:=repository.CreateControl(t.Context(),'+control+');err!=nil {t.Fatal(err)}\n'
 if (checkout/'internal/persistence/sqlite/repository_remote_mirror_test.go').exists():
  extra += '''remote:=remoteSpec(jobSpecForRun(t,runWithCompleteSnapshot(t)))
 remote.JobID="72000000-0000-4000-8000-000000000011"
 remote.Spec.Run.Meta.ID="30000000-0000-4000-8000-000000000016"
 executor:=openOperationalRepository(t); defer executor.Close()
 if _,_,err:=executor.AcceptJob(t.Context(),jobAcceptance(remote));err!=nil {t.Fatal(err)}
 applyRemoteEvents(t,repository,remoteEvents(t,executor,remote))
 '''
 producer=producer.replace('fixture:=newRepositoryFixture(t)', extra+'\n fixture:=newRepositoryFixture(t)')
 (checkout/'internal/persistence/sqlite/migration_export_test.go').write_text(producer,encoding='utf-8')
 database=checkout/('fixture-rich-'+uuid.uuid4().hex+'.sqlite')
 env=os.environ.copy(); env['MIGRATION_FIXTURE_DB']=str(database); env['GOWORK']='off'
 result=subprocess.run(['go','test','./internal/persistence/sqlite','-run','^TestExportHistoricalMigrationFixture$','-count=1'],cwd=checkout,env=env,capture_output=True,text=True,encoding='utf-8')
 print(short,result.returncode,result.stdout[-1500:],result.stderr[-600:],flush=True)
 if result.returncode: raise SystemExit(result.returncode)
 db=sqlite3.connect(database)
 records=[]
 for (table,) in db.execute("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name"):
  if table.startswith('sqlite_') and table!='sqlite_sequence': continue
  cursor=db.execute('SELECT * FROM "'+table+'"')
  rows=[]
  for row in cursor:
   rows.append([{'blob':base64.b64encode(v).decode()} if isinstance(v,bytes) else v for v in row])
  if rows:
   records.append({'table':table,'columns':[c[0] for c in cursor.description],'rows':rows})
 schema = [{'kind':kind,'name':name,'table':table,'ddl':ddl} for kind,name,table,ddl in db.execute("SELECT type,name,tbl_name,sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' ORDER BY rowid")]
 destination.write_text(json.dumps({'source_commit':ref,'schema':schema,'tables':records},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
 db.close()
