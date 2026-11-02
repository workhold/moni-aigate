"""Independently verify fixed R1-A roots from repository bytes; no live metadata.

Run: python3 scripts/check-wallet-profile-roots.py
No files or privileges are modified.
"""
import json,hashlib,copy
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

def canonical(v):
 return json.dumps(v,ensure_ascii=False,sort_keys=True,separators=(',',':')).replace('\u2028','\\u2028').replace('\u2029','\\u2029').encode()
def sha(b): return hashlib.sha256(b).hexdigest()
def ident(v):
 if 'identity' in v: return canonical(v['identity'])
 return canonical({k:v[k] for k in ['object','referenced','role','member','grantor','grantee','privilege','kind','catalog','owner','namespace','object_type','name'] if k in v})
app={'class':'role','scope':'role','name':'application'}
owner={'class':'role','scope':'role','name':'application_owner'}
summary={}
for mode in ['ordinary','sandbox']:
 raw=(ROOT / ('internal/fundinglotcatalog/expected/'+mode+'-v1.json')).read_bytes()
 d=json.loads(raw)
 assert canonical(d)==raw
 before=copy.deepcopy(d)
 roots={}
 for v in d['variants']:
  role=v['roles']['application']
  for sub,col in enumerate(['id','verified_query_id','conflicting_digest','received_at'],1):
   obj={'class':'relation','scope':'application','name':'payment_query_conflicts','sub_id':sub,'column':col}
   entries=[x for x in role['column_acl'] if x['object']==obj]; assert len(entries)==1
   entry=entries[0]; assert entry=={'object':obj,'acl_is_null':True,'entries':[],'default_entries':[]}
   entry['acl_is_null']=False
   entry['entries']=[{'grantor':owner,'grantee':app,'privilege':'SELECT','grant_option':False}]
   eff=[x for x in role['effective_privileges'] if x['object']==obj and x['role']==app]; assert len(eff)==1
   entry=eff[0]; assert entry=={'object':obj,'role':app,'kind':'column','privileges':[False]*4,'grant_options':[False]*4}
   entry['privileges'][0]=True
   dep={'catalog':'pg_shdepend','object':obj,'role':app,'kind':'a','multiplicity':1}
   assert not any(ident(x)==ident(dep) for x in role['acl_dependencies'])
   role['acl_dependencies'].append(dep)
  if mode == 'sandbox':
   for scope in ['mock', 'receiver']:
    for sub,col in enumerate(['id','verified_query_id','conflicting_digest','received_at'],1):
     obj={'class':'relation','scope':'application','name':'payment_query_conflicts','sub_id':sub,'column':col}
     dep={'catalog':'pg_shdepend','object':obj,'role':app,'kind':'a','multiplicity':1}
     rows=v['roles'][scope]['acl_dependencies']
     assert not any(ident(x)==ident(dep) for x in rows)
     rows.append(dep)
    rows.sort(key=ident)
  for k in ['column_acl','effective_privileges','acl_dependencies']:
   role[k].sort(key=ident)
  assert v['schema']==next(x for x in before['variants'] if x['name']==v['name'])['schema']
  roots[v['name']]={'schema':sha(canonical(v['schema'])),'roles':sha(canonical(v['roles']))}
 summary[mode]={'base_sha256':sha(raw),'expected_sha256':sha(canonical(d)),'canonical_bytes':len(canonical(d)),'variants':roots}
reviewed = json.loads((ROOT / 'docs/verification/runtime-read-profile-roots.json').read_text())
if summary != reviewed:
 raise SystemExit('static wallet-read profile roots differ from reviewed artifact')
print('static wallet-read profile roots verified: 2 modes, 6 variants, exact four-column delta')
