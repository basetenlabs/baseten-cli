import http.server,json,os,pathlib,subprocess,tempfile,threading,sys,select,time,errno
# Run against the test-only fixture executable. All credentials and inference are local fixtures.
# OpenCode can finish plugin-cache writes after its CLI process exits.
# Retry only a concurrent directory-not-empty cleanup failure.
class ProbeDirectory(tempfile.TemporaryDirectory):
 def cleanup(self):
  for attempt in range(25):
   try:
    return super().cleanup()
   except OSError as error:
    if error.errno != errno.ENOTEMPTY or attempt == 24:
     raise
    time.sleep(0.2)

cli=str(pathlib.Path(sys.argv[1]).resolve())
catalog=str(pathlib.Path(__file__).with_name('routes.json').resolve())
seen=[]
class H(http.server.BaseHTTPRequestHandler):
 def log_message(self,*x):pass
 def do_POST(self):
  b=json.loads(self.rfile.read(int(self.headers['Content-Length'])));seen.append((self.path,b.get('model')))
  self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
  if 'responses' in self.path:
   native=pathlib.Path(__file__).parent.parent/'templates/codex-0.134.0-prompt.txt'
   assert b.get('instructions')==native.read_text(), 'native Codex instructions changed'
   item={'id':'msg_test','type':'message','role':'assistant','status':'completed','content':[{'type':'output_text','text':'LOCAL_ROUTE_OK','annotations':[]}]}
   response={'id':'resp_test','object':'response','status':'completed','model':b.get('model'),'output':[item],'usage':{'input_tokens':10,'output_tokens':3,'total_tokens':13}}
   events=[{'type':'response.created','response':{**response,'status':'in_progress','output':[]}},{'type':'response.output_item.added','output_index':0,'item':{**item,'status':'in_progress','content':[]}},{'type':'response.content_part.added','item_id':'msg_test','output_index':0,'content_index':0,'part':{'type':'output_text','text':'','annotations':[]}},{'type':'response.output_text.delta','item_id':'msg_test','output_index':0,'content_index':0,'delta':'LOCAL_ROUTE_OK'},{'type':'response.output_item.done','output_index':0,'item':item},{'type':'response.completed','response':response}]
   for i,e in enumerate(events):self.wfile.write(('event: '+e['type']+'\ndata: '+json.dumps({**e,'sequence_number':i})+'\n\n').encode())
  else:
   chunks=[({'role':'assistant','content':'LOCAL_ROUTE_OK'},None),({},'stop')]
   if b.get('model')=='acme/primary' and any(t.get('function',{}).get('name')=='task' for t in b.get('tools',[])) and not any(m.get('role')=='tool' for m in b.get('messages',[])):
    args=json.dumps({'description':'Local route check','prompt':'Return LOCAL_ROUTE_OK without tools','subagent_type':'general'})
    chunks=[({'role':'assistant','tool_calls':[{'index':0,'id':'call_fixture','type':'function','function':{'name':'task','arguments':args}}]},None),({},'tool_calls')]
   for delta,finish in chunks:
    c={'id':'chatcmpl_fixture','object':'chat.completion.chunk','created':1,'model':b.get('model'),'choices':[{'index':0,'delta':delta,'finish_reason':finish}]}
    self.wfile.write(('data: '+json.dumps(c)+'\n\n').encode())
   self.wfile.write(b'data: [DONE]\n\n')
server=http.server.ThreadingHTTPServer(('127.0.0.1',0),H);threading.Thread(target=server.serve_forever,daemon=True).start()
with tempfile.TemporaryDirectory(prefix='harness-extra-') as tmp:
 p=pathlib.Path(tmp);(p/'codex').mkdir();(p/'config').mkdir();base=f'http://127.0.0.1:{server.server_port}/v1'
 env={k:v for k,v in os.environ.items() if k in ['PATH','TMPDIR','LANG']};env.update(HOME=tmp,CODEX_HOME=str(p/'codex'),XDG_CONFIG_HOME=str(p/'config'),XDG_DATA_HOME=str(p/'data'),XDG_CACHE_HOME=str(p/'cache'),XDG_STATE_HOME=str(p/'state'),OPENCODE_CONFIG=str(p/'opencode.json'),OPENCODE_DISABLE_MODELS_FETCH='true',OPENCODE_DISABLE_AUTOUPDATE='true')
 for name,config in [('codex',p/'codex/config.toml'),('opencode',p/'opencode.json')]:
  setup=[cli,'harness','setup','--harness',name,'--config',str(config),'--catalog-fixture',catalog,'--fixture-endpoint',base.removesuffix('/v1'),'--model','acme/primary','--yes','--output','json']
  if name=='opencode':setup+=['--subagent-model','acme/subagent']
  for repeat in range(2):
   run=subprocess.run(setup,env=env,cwd=tmp,capture_output=True,text=True)
   assert run.returncode==0,(run.stdout,run.stderr)
   result=json.loads(run.stdout)
   assert all(x['changed']==(repeat==0) for x in result.get('changes',[result])),result
  run=subprocess.run([cli,'harness','status','--harness',name,'--config',str(config),'--output','json'],env=env,cwd=tmp,capture_output=True,text=True,check=True)
  assert json.loads(run.stdout)['state']=='configured',run.stdout
 # The native app-server catalog is also used by Codex CLI model selection.
 rpc=subprocess.Popen(['codex','app-server'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,text=True,env=env,cwd=tmp)
 try:
  for msg in [{'id':1,'method':'initialize','params':{'clientInfo':{'name':'harness_probe','version':'1'},'capabilities':{'experimentalApi':True}}},{'method':'initialized'},{'id':2,'method':'model/list','params':{}}]:
   rpc.stdin.write(json.dumps(msg)+'\n');rpc.stdin.flush()
   if 'id' not in msg:continue
   while True:
    ready,_,_=select.select([rpc.stdout],[],[],15);assert ready,'Codex catalog RPC timeout'
    response=json.loads(rpc.stdout.readline())
    if response.get('id')==msg['id']:break
   assert 'error' not in response,response
  ids=[m['id'] for m in response['result']['data']]
  assert 'acme/primary' in ids and 'acme/background' in ids,ids
  print(json.dumps({'codex_catalog':ids}),flush=True)
 finally:rpc.terminate();rpc.wait(timeout=5)
 for args in [['codex','exec','--skip-git-repo-check','--ephemeral','--sandbox','read-only','Return OK'],['opencode','models','baseten-harness'],['opencode','run','--pure','--format','json','Return OK']]:
  try:
   r=subprocess.run(args,env=env,cwd=tmp,capture_output=True,text=True,timeout=45);assert r.returncode==0,(args,r.stdout,r.stderr)
   if 'models' in args:assert 'baseten-harness/acme/primary' in r.stdout
   else:assert 'LOCAL_ROUTE_OK' in r.stdout,(args,r.stdout,r.stderr)
   print(json.dumps({'args':args,'exit':r.returncode}),flush=True)
  except subprocess.TimeoutExpired:raise AssertionError('mock inference timed out: '+str(args))
 for name,config in [('codex',p/'codex/config.toml'),('opencode',p/'opencode.json')]:
  r=subprocess.run([cli,'harness','teardown','--harness',name,'--config',str(config),'--yes'],env=env,cwd=tmp,capture_output=True,text=True)
  assert r.returncode==0,(r.stdout,r.stderr)
  # Native harnesses can add their own unrelated settings during use.
  if config.exists():
   remaining=config.read_text()
   assert 'baseten-harness' not in remaining,remaining
  assert not pathlib.Path(str(config)+'.baseten-harness.json').exists()
 assert not (p/'codex/config.toml.baseten-models.json').exists()
assert ('/v1/responses','acme/primary') in seen,seen
assert ('/v1/chat/completions','acme/primary') in seen,seen
assert ('/v1/chat/completions','deepseek-ai/DeepSeek-V4.1-Flash') in seen,seen
assert ('/v1/chat/completions','acme/subagent') in seen,seen
print(json.dumps(seen));server.shutdown()
