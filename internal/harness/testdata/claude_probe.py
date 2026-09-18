import http.server, json, os, pathlib, subprocess, sys, tempfile, threading

# Optional local integration test: python3 claude_probe.py /absolute/path/to/harness-fixture
# Uses only dummy auth and a loopback mock, with isolated HOME and config.
cli = str(pathlib.Path(sys.argv[1]).resolve())
catalog = str(pathlib.Path(__file__).with_name("routes.json").resolve())
requests=[]
mode='primary'
class Handler(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args): pass
 def do_POST(self):
  b=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
  model=b.get('model'); requests.append({'mode':mode,'path':self.path,'model':model})
  if 'count_tokens' in self.path:
   self.send_response(200); self.end_headers(); self.wfile.write(b'{"input_tokens":20}'); return
  if mode=='fallback' and model=='acme/primary':
   self.send_response(503); self.send_header('Content-Type','application/json'); self.end_headers(); self.wfile.write(b'{"type":"error","error":{"type":"api_error","message":"mock unavailable"}}'); return
  content=[{'type':'text','text':'LOCAL_ROUTE_OK'}]; stop='end_turn'
  if mode=='subagent' and model=='acme/primary' and not any(x.get('type')=='tool_result' for m in b.get('messages',[]) if isinstance(m.get('content'),list) for x in m['content']):
   names=[x['name'] for x in b.get('tools',[])]; tool=next((x for x in names if x in ('Agent','Task')),None)
   if tool: content=[{'type':'tool_use','id':'tool_fixture','name':tool,'input':{'description':'Local route probe','prompt':'Return LOCAL_ROUTE_OK only. Do not use tools.','subagent_type':'general-purpose'}}]; stop='tool_use'
  msg={'id':'msg_fixture','type':'message','role':'assistant','model':model,'content':content,'stop_reason':stop,'stop_sequence':None,'usage':{'input_tokens':20,'output_tokens':5}}
  self.send_response(200)
  if b.get('stream'):
   self.send_header('Content-Type','text/event-stream'); self.end_headers()
   def event(t,d): self.wfile.write(('event: '+t+'\ndata: '+json.dumps({'type':t,**d})+'\n\n').encode())
   event('message_start',{'message':{**msg,'content':[],'stop_reason':None}})
   for i,c in enumerate(content):
    event('content_block_start',{'index':i,'content_block':{**c,**({'text':''} if c['type']=='text' else {'input':{}})}})
    event('content_block_delta',{'index':i,'delta':{'type':'text_delta','text':c['text']} if c['type']=='text' else {'type':'input_json_delta','partial_json':json.dumps(c['input'])}})
    event('content_block_stop',{'index':i})
   event('message_delta',{'delta':{'stop_reason':stop,'stop_sequence':None},'usage':{'output_tokens':5}}); event('message_stop',{})
  else:
   self.send_header('Content-Type','application/json'); self.end_headers(); self.wfile.write(json.dumps(msg).encode())
server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
threading.Thread(target=server.serve_forever,daemon=True).start()
with tempfile.TemporaryDirectory(prefix='baseten-harness-probe-') as tmp:
 p=pathlib.Path(tmp); (p/'home').mkdir(); (p/'config').mkdir()
 env={k:v for k,v in os.environ.items() if k in ['PATH','TMPDIR','LANG']}
 env.update(HOME=str(p/'home'),CLAUDE_CONFIG_DIR=str(p/'config'),ANTHROPIC_BASE_URL=f'http://127.0.0.1:{server.server_port}',ANTHROPIC_AUTH_TOKEN='local-dummy',CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1',DISABLE_AUTOUPDATER='1',CLAUDE_CODE_MAX_RETRIES='0')
 f=p/'settings.json'
 setup=[cli,'harness','setup','--harness','claude-code','--config',str(f),'--catalog-fixture',catalog,'--fixture-endpoint',f'http://127.0.0.1:{server.server_port}','--model','acme/primary','--background-model','acme/background','--subagent-model','acme/subagent','--fallback-model','acme/fallback','--replace-picker','--yes','--output','json']
 for repeat in range(2):
  applied=subprocess.run(setup,env=env,cwd=tmp,capture_output=True,text=True,check=True)
  result=json.loads(applied.stdout)
  assert result['changed'] == (repeat==0), result
 status=subprocess.run([cli,'harness','status','--config',str(f),'--output','json'],env=env,cwd=tmp,capture_output=True,text=True,check=True)
 assert json.loads(status.stdout)['state']=='configured'
 for mode in ['primary','background','subagent','fallback']:
  args=['claude','-p','Local fixture. Return OK.','--settings',str(f),'--setting-sources','','--no-session-persistence','--output-format','json','--max-turns','4','--permission-mode','dontAsk']
  if mode=='background': args+=['--model','haiku']
  if mode=='subagent': args+=['--allowedTools','Agent']
  else: args+=['--tools','']
  try:
   r=subprocess.run(args,env=env,cwd=tmp,capture_output=True,text=True,timeout=40)
   assert r.returncode == 0, (mode,r.stderr,r.stdout)
   assert "This row was ignored" not in r.stderr+r.stdout, (mode,r.stderr,r.stdout)
   outputs=[json.loads(line) for line in r.stdout.splitlines() if line.startswith('{')]
   assert any(x.get('result')=='LOCAL_ROUTE_OK' for x in outputs), (mode,r.stdout)
   print(json.dumps({'mode':mode,'exit':r.returncode,'models':sorted({x['model'] for x in requests if x['mode']==mode})}),flush=True)
  except subprocess.TimeoutExpired: raise AssertionError('local mock timed out: '+mode)
 subprocess.run([cli,'harness','teardown','--config',str(f),'--yes'],env=env,cwd=tmp,capture_output=True,text=True,check=True)
 assert not f.exists()
 assert not pathlib.Path(str(f)+'.baseten-harness.json').exists()
for scenario,expected in [('primary','acme/primary'),('background','acme/background'),('subagent','acme/subagent'),('fallback','acme/fallback')]:
 assert any(x['mode']==scenario and x['model']==expected for x in requests), (scenario,requests)
server.shutdown()
print(json.dumps(requests,indent=2))
