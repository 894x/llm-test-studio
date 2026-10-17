import subprocess, tarfile, io, json, sqlite3, os, base64, uuid
from pathlib import Path

root=Path.cwd().resolve()
catalog=json.loads((root/'internal/persistence/sqlite/migrations/operational_baselines.json').read_text())
out=root/'internal/persistence/sqlite/testdata/migrations'
out.mkdir(parents=True,exist_ok=True)
test=r'''package sqlite_test
import ("os"; "testing"; "time"; "github.com/894x/llm-test-studio/internal/domain")
func TestExportHistoricalMigrationFixture(t *testing.T) {
 fixture:=newPerformanceStorageFixture(t)
 fixture.start(t); fixture.recordThrough(t,6)
 terminal:=fixture.change(t,domain.RunCompleted)
 if _,err:=fixture.repository.SealPerformanceResult(t.Context(),fixture.spec.JobID,terminal.UpdatedAt.Add(time.Second));err!=nil {t.Fatal(err)}
 if err:=fixture.repository.Close();err!=nil {t.Fatal(err)}
 if err:=fixture.db.Close();err!=nil {t.Fatal(err)}
 data,err:=os.ReadFile(fixture.path);if err!=nil {t.Fatal(err)}
 if err:=os.WriteFile(os.Getenv("MIGRATION_FIXTURE_DB"),data,0600);err!=nil {t.Fatal(err)}
}
'''
for baseline in catalog['baselines']:
 ref=baseline['commit']; short=ref[:8]
 if short not in ('0dc44b1b','8613f4bd'): continue
 destination=out/(short+'-performance.json')
 if destination.exists() and not os.environ.get("REBUILD_FIXTURES"): continue
 checkout=root/'.tmp/migration-producers'/short
 checkout.mkdir(parents=True,exist_ok=True)
 archive=subprocess.check_output(['git','archive',ref])
 with tarfile.open(fileobj=io.BytesIO(archive)) as tf:
  tf.extractall(checkout,filter='data')
 (checkout/'internal/persistence/sqlite/migration_export_test.go').write_text(test,encoding='utf-8')
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
 destination.write_text(json.dumps({'source_commit':ref,'tables':records},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
 db.close()
