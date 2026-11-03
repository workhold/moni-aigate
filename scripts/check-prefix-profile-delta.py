"""Read-only static PB-prefix PG16 descriptor derivation; no DB access.

The reviewed input is original authenticated catalog bytes and candidate SQL.
This compiler uses fixed PG16 catalog rules and original object templates.
It checks the reviewed record delta and full roots without rewriting either.
Run: python3 scripts/check-prefix-profile-delta.py
"""
import copy
import hashlib
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent

def canonical(v):
    return json.dumps(v, ensure_ascii=False, sort_keys=True, separators=(',', ':')).replace('\u2028', '\\u2028').replace('\u2029', '\\u2029').encode()

def sha(v):
    return hashlib.sha256(v).hexdigest()

def ident(v):
    if 'identity' in v:
        return canonical(v['identity'])
    return canonical({k: v[k] for k in ['object','referenced','role','member','grantor','grantee','privilege','kind','catalog','owner','namespace','object_type','name'] if k in v})

def obj(cls, name, **extra):
    return dict({'class': cls, 'scope': 'application', 'name': name}, **extra)

APP = {'class': 'role', 'scope': 'role', 'name': 'application'}
OWNER = {'class': 'role', 'scope': 'role', 'name': 'application_owner'}
PUBLIC = {'class': 'role', 'scope': 'public', 'name': 'PUBLIC'}
NS = obj('namespace', 'application')
PREFIX = 'periodic_budget_observation_'
ORIGINS, HEADS, JOURNAL = [PREFIX + x for x in ['origins','heads','journal']]
SQL = (ROOT / 'internal/periodicbudgetprefixschema/materials/candidate-v1.sql').read_bytes()
ACL = (ROOT / 'internal/periodicbudgetprefixschema/materials/permissions-v1.sql').read_bytes()
assert sha(SQL) == 'b225c3ff61b735d6830aed1d25edc27bc4784f8e251cb2c1d656196d8d9c630e'
assert sha(ACL) == 'aaea2994d5a79726df8b5c1767b5adb054d14ee8cb81fa12b43b87d24a2f28bf'

def pg_name(table, cols, label):
    # PostgreSQL makeObjectName, ASCII-only reviewed names, NAMEDATALEN=64.
    a, b = len(table), len(cols)
    available = 63 - len(label) - bool(label) - bool(cols)
    while a+b > available:
        if a > b:
            a -= 1
        else:
            b -= 1
    return '_'.join(x for x in [table[:a], cols[:b], label] if x)

def wallet(d):
    for v in d['variants']:
        r = v['roles']['application']
        for i, name in enumerate(['id','verified_query_id','conflicting_digest','received_at'],1):
            col = obj('relation','payment_query_conflicts',sub_id=i,column=name)
            acl = next(x for x in r['column_acl'] if x['object'] == col)
            assert acl == {'object':col,'acl_is_null':True,'entries':[],'default_entries':[]}
            acl['acl_is_null'] = False
            acl['entries'] = [{'grantor':OWNER,'grantee':APP,'privilege':'SELECT','grant_option':False}]
            eff = next(x for x in r['effective_privileges'] if x['object']==col and x['role']==APP)
            assert eff['privileges']==[False]*4
            eff['privileges'][0] = True
            dep = {'catalog':'pg_shdepend','object':col,'role':APP,'kind':'a','multiplicity':1}
            for scope in v['roles']:
                v['roles'][scope]['acl_dependencies'].append(copy.deepcopy(dep))
        for scope, role in v['roles'].items():
            for cat in ['column_acl','effective_privileges','acl_dependencies']:
                role[cat].sort(key=ident)
    return d

def transform(v):
    s = v['schema']['application']
    r = v['roles']['application']
    before = copy.deepcopy(v)
    rels = {x['identity']['name']:x for x in s['relations'] if x['identity']['scope']=='application'}
    types = {x['identity']['name']:x for x in s['types']}
    source = rels['periodic_budget_controls']
    attrs_by_type = {}
    for table in rels.values():
        for a in table['attributes']:
            if a['type'].get('namespace')=='pg_catalog':
                attrs_by_type.setdefault(a['type']['name'], copy.deepcopy(a))
    def attribute(name, typ, notnull=False, default=None, generated=''):
        a = copy.deepcopy(attrs_by_type[typ])
        a.update(name=name, not_null=notnull, default=default, generated=generated, attnum=0)
        return a
    def dep(o, ref, kind='n', catalog='pg_depend'):
        s['dependencies'].append({'catalog':catalog,'object':o,'referenced':ref,'kind':kind,'multiplicity':1})
    def column(table, name):
        a = next(x for x in rels[table]['attributes'] if x['name']==name)
        return obj('relation',table,sub_id=a['attnum'],column=name)
    def add_attrs(table, defs):
        target = rels[table]
        for name, typ, nn, default, generated in defs:
            a = attribute(name,typ,nn,default,generated)
            a['attnum'] = len(target['attributes'])+1
            target['attributes'].append(a)
    defs = {
        ORIGINS:[('control_id','text',True,None,''),('version','int4',True,'1',''),('created_xid','xid8',True,'pg_current_xact_id()',''),('tracking_started_at','timestamptz',True,None,'')],
        HEADS:[('control_id','text',True,None,''),('ordinal','int8',True,'0',''),('last_observation_id','text',False,None,''),('updated_xid','xid8',True,'pg_current_xact_id()','')],
        JOURNAL:[('id','text',True,None,''),('control_id','text',True,None,''),('ordinal','int8',True,None,''),('previous_id','text',False,None,''),('kind','text',True,None,''),('source_id','text',True,None,''),('event_seq','int8',True,None,''),('last_event_id','text',False,None,''),('configuration_revision','int8',True,None,''),('configuration_operation_id','text',True,None,''),('window_id','text',False,None,''),('window_revision','int8',False,None,''),('window_last_event_id','text',False,None,''),('created_xid','xid8',True,'pg_current_xact_id()',''),('observed_at','timestamptz',False,None,''),('command_source_id','text',False,"\nCASE\n    WHEN (kind = 'command'::text) THEN source_id\n    ELSE NULL::text\nEND",'s'),('window_source_id','text',False,"\nCASE\n    WHEN (kind = 'window_birth'::text) THEN source_id\n    ELSE NULL::text\nEND",'s')]
    }
    auto_templates = [x for x in s['relations'] if x['identity'].get('scope')=='automatic' and x['identity'].get('owner')==obj('relation','periodic_budget_controls')]
    toast_index_template = next(x for x in s['indexes'] if x['identity'].get('scope')=='automatic' and x['identity'].get('owner')==obj('relation','periodic_budget_controls'))
    def rename_tree(value, old, new):
        if isinstance(value,dict):
            return {k:rename_tree(x,old,new) for k,x in value.items()}
        if isinstance(value,list):
            return [rename_tree(x,old,new) for x in value]
        if value==old:
            return new
        if value=='_'+old:
            return '_'+new
        return value
    for table, fields in defs.items():
        new = rename_tree(copy.deepcopy(source),'periodic_budget_controls',table)
        new['attributes'] = []
        rels[table]=new
        s['relations'].append(new)
        add_attrs(table,fields)
        for record in auto_templates:
            s['relations'].append(rename_tree(record,'periodic_budget_controls',table))
        s['indexes'].append(rename_tree(toast_index_template,'periodic_budget_controls',table))
        for name in ['periodic_budget_controls','_periodic_budget_controls']:
            s['types'].append(rename_tree(types[name],'periodic_budget_controls',table))
        dep(obj('relation',table),NS)
        dep(obj('relation',table),OWNER,'o','pg_shdepend')
        dep(obj('type',table),obj('relation',table),'i')
        dep(obj('type','_'+table),obj('type',table),'i')
        heap = {'class':'relation','scope':'automatic','name':'toast_heap','owner':obj('relation',table)}
        index = {'class':'relation','scope':'automatic','name':'toast_index','owner':obj('relation',table)}
        dep(heap,obj('relation',table),'i')
        for i,name in enumerate(['chunk_id','chunk_seq'],1):
            dep(index,dict(heap,sub_id=i,column=name),'a')
        for name,typ,nn,default,generated in fields:
            if default is not None:
                a=column(table,name)
                default_ref=obj('default',table+'/'+str(a['sub_id']))
                dep(default_ref,a,'i' if generated else 'a')
                if generated:
                    for field in ['kind','source_id']:
                        dep(default_ref,column(table,field))
    add_attrs('periodic_budget_operations',[('observation_id','text',False,None,'')])
    add_attrs('periodic_budget_windows',[('observation_id','text',False,None,''),('observation_created_xid','xid8',False,None,'')])

    # Constraint fields and deparse expressions are fixed reviewed PG16 values.
    constraint_templates = {kind:next(x for x in s['constraints'] if x['kind']==kind) for kind in ['c','p','u','f','t']}
    funcs = {x['identity']['name']:x for x in s['functions']}
    uuid_ref = funcs['pb_uuid']['identity']
    def check(table, name, cols, expr, uuid=False):
        c=copy.deepcopy(constraint_templates['c'])
        c.update(identity=obj('constraint',table+'/'+name),relation=obj('relation',table),columns=[column(table,x)['sub_id'] for x in cols],expression=expr)
        s['constraints'].append(c)
        for name in cols:
            dep(c['identity'],column(table,name),'a')
            dep(c['identity'],column(table,name),'n')
        if uuid:
            dep(c['identity'],uuid_ref)
    check('periodic_budget_operations','pb_operation_observation_uuid',['observation_id'],'((observation_id IS NULL) OR pb_uuid(observation_id))',True)
    check('periodic_budget_windows','pb_window_observation_shape',['observation_id','observation_created_xid'],'(((observation_id IS NULL) AND (observation_created_xid IS NULL)) OR (pb_uuid(observation_id) AND (observation_created_xid IS NOT NULL)))',True)
    for table in [ORIGINS,HEADS,JOURNAL]:
        idcol='id' if table==JOURNAL else 'control_id'
        check(table,pg_name(table,idcol,'check'),[idcol],f'pb_uuid({idcol})',True)
    check(ORIGINS,pg_name(ORIGINS,'version','check'),['version'],'(version = 1)')
    check(ORIGINS,pg_name(ORIGINS,'tracking_started_at','check'),['tracking_started_at'],'isfinite(tracking_started_at)')
    check(HEADS,pg_name(HEADS,'ordinal','check'),['ordinal'],'(ordinal >= 0)')
    check(HEADS,pg_name(HEADS,'','check'),['ordinal','last_observation_id'],'(((ordinal = 0) AND (last_observation_id IS NULL)) OR ((ordinal > 0) AND pb_uuid(last_observation_id)))',True)
    for name in ['control_id','source_id','configuration_operation_id']:
        check(JOURNAL,pg_name(JOURNAL,name,'check'),[name],f'pb_uuid({name})',True)
    for name in ['previous_id','last_event_id','window_id','window_last_event_id']:
        check(JOURNAL,pg_name(JOURNAL,name,'check'),[name],f'(({name} IS NULL) OR pb_uuid({name}))',True)
    for name,op in [('ordinal','>'),('event_seq','>='),('configuration_revision','>'),('window_revision','>=')]:
        check(JOURNAL,pg_name(JOURNAL,name,'check'),[name],f'({name} {op} 0)')
    check(JOURNAL,pg_name(JOURNAL,'kind','check'),['kind'],"(kind = ANY (ARRAY['window_birth'::text, 'command'::text]))")
    check(JOURNAL,pg_name(JOURNAL,'','check'),['event_seq','last_event_id'],'(((event_seq = 0) AND (last_event_id IS NULL)) OR ((event_seq > 0) AND (last_event_id IS NOT NULL)))')
    check(JOURNAL,pg_name(JOURNAL,'','check1'),['window_id','window_revision','window_last_event_id'],'(((window_id IS NULL) AND (window_revision IS NULL) AND (window_last_event_id IS NULL)) OR ((window_id IS NOT NULL) AND (window_revision IS NOT NULL) AND (((window_revision = 0) AND (window_last_event_id IS NULL)) OR ((window_revision > 0) AND (window_last_event_id IS NOT NULL)))))')
    check(JOURNAL,pg_name(JOURNAL,'','check2'),['kind','window_id','source_id','window_revision','window_last_event_id','observed_at'],"((((kind = 'window_birth'::text) AND (window_id = source_id) AND (window_revision = 0) AND (window_last_event_id IS NULL) AND (observed_at IS NULL)) OR ((kind = 'command'::text) AND (observed_at IS NOT NULL) AND isfinite(observed_at))) IS TRUE)")

    # All new indexes are ordinary nondeferrable btree PK/UNIQUE indexes.
    index_template=next(x for x in s['indexes'] if x['identity']==obj('relation','periodic_budget_controls_pkey'))
    index_relation_template=rels['periodic_budget_controls_pkey']
    opclasses={}
    for ix in s['indexes']:
        for i,k in enumerate(ix['keys']):
            if k>0 and ix['relation']['scope']=='application' and ix['relation']['name'] in rels:
                at=rels[ix['relation']['name']]['attributes'][k-1]
                opclasses.setdefault(at['type'].get('name'),ix['opclasses'][i])
    opclasses['xid8']={'class':'operator class','scope':'pg_catalog','name':'xid8_ops','identity':'pg_catalog.xid8_ops USING btree','oid':10053,'sub_id':0}
    indexes={}
    def unique(table,name,cols,primary=False):
        index=obj('relation',name)
        c=copy.deepcopy(constraint_templates['p' if primary else 'u'])
        c.update(identity=obj('constraint',table+'/'+name),relation=obj('relation',table),columns=[column(table,x)['sub_id'] for x in cols],index=index)
        s['constraints'].append(c)
        for name in cols:
            dep(c['identity'],column(table,name),'a')
        dep(index,c['identity'],'i')
        ix=copy.deepcopy(index_template)
        ix.update(identity=index,relation=obj('relation',table),attribute_count=len(cols),key_count=len(cols),keys=c['columns'],primary=primary,options=[0]*len(cols),collations=[],opclasses=[])
        ir=copy.deepcopy(index_relation_template)
        ir.update(identity=index,attributes=[])
        for i,name in enumerate(cols,1):
            a=copy.deepcopy(rels[table]['attributes'][column(table,name)['sub_id']-1])
            a.update(attnum=i,not_null=False,default=None,generated='')
            ir['attributes'].append(a)
            ix['collations'].append(a['collation'])
            ix['opclasses'].append(opclasses[a['type']['name']])
        s['indexes'].append(ix)
        s['relations'].append(ir)
        indexes[(table,tuple(cols))]=index
    for table in [ORIGINS,HEADS,JOURNAL]:
        unique(table,pg_name(table,'','pkey'),['id' if table==JOURNAL else 'control_id'],True)
    for cols in [['control_id','ordinal'],['kind','source_id'],['id','control_id','ordinal','created_xid'],['id','control_id','command_source_id','created_xid','observed_at'],['id','control_id','window_source_id','created_xid']]:
        unique(JOURNAL,pg_name(JOURNAL,'_'.join(cols),'key'),cols)
    for table,name,cols in [
        ('periodic_budget_operations','pb_operation_observation_identity',['operation_id','resource_id','observation_id','created_xid','created_at']),
        ('periodic_budget_windows','pb_window_observation_identity',['id','control_id','observation_id','observation_created_xid']),
        ('periodic_budget_configuration_versions','pb_version_observation_identity',['control_id','revision','operation_id']),
        ('periodic_budget_events','pb_event_observation_identity',['control_id','seq','id'])]:
        unique(table,name,cols)
    # Original FK target indexes are resolved by their exact ordered key set.
    for ix in s['indexes']:
        table=ix['relation']['name']
        if ix['unique'] and table in rels and ix['predicate'] is None and all(k>0 for k in ix['keys']):
            cols=tuple(rels[table]['attributes'][k-1]['name'] for k in ix['keys'])
            indexes.setdefault((table,cols),ix['identity'])
    eqops={}
    for c in s['constraints']:
        if c['kind']=='f':
            table=c['relation']['name']
            for i,k in enumerate(c['columns']):
                eqops.setdefault(rels[table]['attributes'][k-1]['type']['name'],c['equality_operators'][i])
    for typ,oid in [('xid8',5068),('timestamptz',1320)]:
        spelling='timestamp with time zone' if typ=='timestamptz' else 'pg_catalog.xid8'
        signature='pg_catalog.=('+spelling+','+spelling+')'
        eqops[typ]={'class':'operator','scope':'pg_catalog','name':signature,'identity':signature,'oid':oid,'sub_id':0}
    fk_templates={}
    for t in s['triggers']:
        if t['internal'] and t['function']['name'].startswith('pg_catalog."RI_FKey_'):
            fk_templates.setdefault(t['type'],t)
    fk_specs=[
        (ORIGINS,'pb_observation_origin_control',['control_id'],'periodic_budget_controls',['id']),
        (HEADS,'pb_observation_head_origin',['control_id'],ORIGINS,['control_id']),
        (ORIGINS,'pb_observation_origin_head',['control_id'],HEADS,['control_id']),
        (JOURNAL,'pb_observation_journal_origin',['control_id'],ORIGINS,['control_id']),
        (JOURNAL,'pb_observation_previous',['previous_id'],JOURNAL,['id']),
        (JOURNAL,'pb_observation_window',['control_id','window_id'],'periodic_budget_windows',['control_id','id']),
        (JOURNAL,'pb_observation_command_source',['command_source_id','control_id','id','created_xid','observed_at'],'periodic_budget_operations',['operation_id','resource_id','observation_id','created_xid','created_at']),
        (JOURNAL,'pb_observation_birth_source',['window_source_id','control_id','id','created_xid'],'periodic_budget_windows',['id','control_id','observation_id','observation_created_xid']),
        ('periodic_budget_operations','pb_operation_observation_source',['observation_id','resource_id','operation_id','created_xid','created_at'],JOURNAL,['id','control_id','command_source_id','created_xid','observed_at']),
        ('periodic_budget_windows','pb_window_observation_source',['observation_id','control_id','id','observation_created_xid'],JOURNAL,['id','control_id','window_source_id','created_xid']),
        (JOURNAL,'pb_observation_configuration',['control_id','configuration_revision','configuration_operation_id'],'periodic_budget_configuration_versions',['control_id','revision','operation_id']),
        (JOURNAL,'pb_observation_event_cut',['control_id','event_seq','last_event_id'],'periodic_budget_events',['control_id','seq','id']),
        (HEADS,'pb_observation_head_tail',['last_observation_id','control_id','ordinal','updated_xid'],JOURNAL,['id','control_id','ordinal','created_xid'])]
    for table,name,cols,target,targetcols in fk_specs:
        c=copy.deepcopy(constraint_templates['f'])
        c.update(identity=obj('constraint',table+'/'+name),relation=obj('relation',table),columns=[column(table,x)['sub_id'] for x in cols],foreign_relation=obj('relation',target),foreign_columns=[column(target,x)['sub_id'] for x in targetcols],index=indexes[(target,tuple(targetcols))],deferrable=True,deferred=True)
        operators=[eqops[rels[table]['attributes'][k-1]['type']['name']] for k in c['columns']]
        for field in ['equality_operators','pk_equality_operators','fk_equality_operators']:
            c[field]=operators
        s['constraints'].append(c)
        for name in cols:
            dep(c['identity'],column(table,name),'a')
        for name in targetcols:
            dep(c['identity'],column(target,name))
        dep(c['identity'],c['index'])
        for typ,fn in [(9,'RI_FKey_noaction_del'),(17,'RI_FKey_noaction_upd'),(5,'RI_FKey_check_ins'),(17,'RI_FKey_check_upd')]:
            t=copy.deepcopy(next(x for x in s['triggers'] if x['internal'] and x['type']==typ and x['function']['name'].startswith('pg_catalog."'+fn+'"')))
            host,peer=(target,table) if fn.startswith('RI_FKey_noaction') else (table,target)
            t.update(identity=obj('trigger',host+'/'+c['identity']['name'].split('/')[-1]+'/'+table+'/'+fn+'/'+str(typ)),relation=obj('relation',host),constraint=c['identity'],constraint_relation=obj('relation',peer),constraint_index=c['index'],deferrable=True,deferred=True)
            s['triggers'].append(t)
            dep(t['identity'],c['identity'],'i')

    body_pattern=re.compile(r'CREATE FUNCTION (pb_prefix_[a-z_]+)\((.*?)\)\s*RETURNS (trigger|text) LANGUAGE plpgsql VOLATILE SECURITY INVOKER AS \$\$(.*?)\$\$;',re.S)
    newfuncs=[]
    for m in body_pattern.finditer(SQL.decode()):
        name,args,result,body=m.groups()
        f=copy.deepcopy(funcs['pb_check_configuration'])
        argnames,argtypes=[],[]
        for arg in args.split(',') if args.strip() else []:
            n,t=arg.strip().split()
            argnames.append(n)
            argtypes.append({'name':{'bigint':'int8','timestamptz':'timestamptz'}.get(t,t),'namespace':'pg_catalog','oid':{'text':25,'bigint':20,'timestamptz':1184}[t]})
        f.update(identity=obj('function',name,identity_arguments=argtypes),argument_types=argtypes,argument_names=argnames or None,result={'namespace':'pg_catalog','name':result,'oid':2279 if result=='trigger' else 25},prosrc=body,source_sha256=sha(body.encode()))
        s['functions'].append(f)
        newfuncs.append(f)
        dep(f['identity'],NS)
        dep(f['identity'],f['language'])
        dep(f['identity'],OWNER,'o','pg_shdepend')
    assert len(newfuncs)==12
    trigger_template=next(x for x in s['triggers'] if not x['internal'] and x['constraint'] is None and x['when'] is None)
    for m in re.finditer(r'CREATE (CONSTRAINT )?TRIGGER (\w+)\s+(BEFORE|AFTER) (.*?) ON (\w+)\s+(DEFERRABLE INITIALLY DEFERRED\s+)?FOR EACH (ROW|STATEMENT) EXECUTE FUNCTION (\w+)\(\);',SQL.decode(),re.S):
        constraint,name,timing,events,table,deferred,level,function=m.groups()
        typ=(1 if level=='ROW' else 0)+(2 if timing=='BEFORE' else 0)+sum({'INSERT':4,'DELETE':8,'UPDATE':16,'TRUNCATE':32}[e.strip()] for e in events.split(' OR '))
        t=copy.deepcopy(trigger_template)
        ref=obj('trigger',table+'/'+name)
        f=next(x for x in newfuncs if x['identity']['name']==function)
        t.update(identity=ref,relation=obj('relation',table),function=f['identity'],type=typ,deferrable=bool(deferred),deferred=bool(deferred),constraint=obj('constraint',table+'/'+name) if constraint else None,constraint_index=None,constraint_relation=None)
        s['triggers'].append(t)
        dep(ref,obj('relation',table),'a')
        dep(ref,f['identity'])
        if constraint:
            c=copy.deepcopy(constraint_templates['t'])
            c.update(identity=t['constraint'],relation=obj('relation',table))
            s['constraints'].append(c)
            dep(c['identity'],obj('relation',table),'a')
            dep(c['identity'],ref,'i')

    def entry(role,privilege):
        return {'grantor':OWNER,'grantee':role,'privilege':privilege,'grant_option':False}
    def acl_record(ref,entries,defaults,null=False):
        return {'object':ref,'acl_is_null':null,'entries':sorted(entries,key=canonical),'default_entries':sorted(defaults,key=canonical)}
    def acldep(ref):
        record={'catalog':'pg_shdepend','object':ref,'role':APP,'kind':'a','multiplicity':1}
        for role in v['roles'].values():
            role['acl_dependencies'].append(copy.deepcopy(record))
    def effective(ref,kind,privileges):
        for role in r['roles']:
            who=role['identity']
            owner=who==OWNER
            ps=[True]*len(privileges) if owner else privileges if who==APP else [False]*len(privileges)
            r['effective_privileges'].append({'object':ref,'kind':kind,'role':who,'privileges':ps,'grant_options':[owner]*len(ps)})
    insert={ORIGINS:{'control_id','version','tracking_started_at'},HEADS:{'control_id','ordinal','last_observation_id'},JOURNAL:{x[0] for x in defs[JOURNAL]}-{'created_xid','command_source_id','window_source_id'}}
    update={HEADS:{'ordinal','last_observation_id'}}
    for table in defs:
        ref=obj('relation',table)
        defaults=[entry(OWNER,x) for x in ['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER']]
        r['relation_acl'].append(acl_record(ref,defaults+[entry(APP,'SELECT')],defaults))
        acldep(ref)
        effective(ref,'table',[True,False,False,False,False,False,False])
        for name in [table,'_'+table]:
            ref=obj('type',name)
            defaults=[entry(PUBLIC,'USAGE'),entry(OWNER,'USAGE')]
            r['type_acl'].append(acl_record(ref,[entry(APP,'USAGE'),entry(OWNER,'USAGE')] if name==table else [],defaults,null=name!=table))
            effective(ref,'type',[True])
            if name==table:
                acldep(ref)
    oldrelids={canonical(x['identity']) for x in before['schema']['application']['relations']}
    for rel in s['relations']:
        isnew=canonical(rel['identity']) not in oldrelids
        for attr in rel['attributes']:
            ref=dict(rel['identity'],sub_id=attr['attnum'],column=attr['name'])
            oldsource=rel['identity'].get('name') in ['periodic_budget_operations','periodic_budget_windows'] and attr['name'] in ['observation_id','observation_created_xid']
            if not isnew and not oldsource:
                continue
            tab=rel['identity']['name']
            entries=[]
            for priv,mapping in [('INSERT',insert),('UPDATE',update)]:
                if attr['name'] in mapping.get(tab,set()):
                    entries.append(entry(APP,priv))
            r['column_acl'].append(acl_record(ref,entries,[],null=not entries))
            if entries:
                acldep(ref)
            if rel['kind']=='r':
                if oldsource:
                    original=next(x for x in before['schema']['application']['relations'] if x['identity']==rel['identity'])
                    oldref=dict(rel['identity'],sub_id=1,column=original['attributes'][0]['name'])
                    for old in before['roles']['application']['effective_privileges']:
                        if old['kind']=='column' and old['object']==oldref:
                            new=copy.deepcopy(old)
                            new['object']=ref
                            r['effective_privileges'].append(new)
                else:
                    effective(ref,'column',[True,attr['name'] in insert[tab],attr['name'] in update.get(tab,set()),False])
    for f in newfuncs:
        defaults=[entry(PUBLIC,'EXECUTE'),entry(OWNER,'EXECUTE')]
        r['function_acl'].append(acl_record(f['identity'],[entry(APP,'EXECUTE'),entry(OWNER,'EXECUTE')],defaults))
        effective(f['identity'],'function',[True])
        acldep(f['identity'])
    for family in ['schema','roles']:
        for scope,doc in v[family].items():
            for cat,records in doc.items():
                if isinstance(records,list):
                    records.sort(key=ident)
                    ids=[ident(x) for x in records]
                    assert len(set(ids))==len(ids),(family,scope,cat,'duplicate')
    a=before['schema']['application']
    for cat,delta in [('relations',21),('types',6),('functions',12),('constraints',55),('indexes',15),('triggers',71)]:
        assert len(s[cat])-len(a[cat])==delta,(cat,len(s[cat])-len(a[cat]),delta)
    changes=[]
    for family in ['schema','roles']:
        for scope,doc in v[family].items():
            for cat,records in doc.items():
                if not isinstance(records,list):
                    assert records==before[family][scope][cat]
                    continue
                old={ident(x):x for x in before[family][scope][cat]}
                now={ident(x):x for x in records}
                removes=[old[k] for k in sorted(old) if k not in now or old[k]!=now[k]]
                adds=[now[k] for k in sorted(now) if k not in old or old[k]!=now[k]]
                if removes or adds:
                    changes.append({'family':family,'scope':scope,'category':cat,'remove':removes,'add':adds})
    return changes

delta={'format_version':1,'profile':3,'postgres_major':16,'sql_sha256':sha(SQL),'permissions_sha256':sha(ACL),'modes':{}}
summary={}
for mode,root in [('ordinary','c9466842df4a55490f53b7c0ba9694ee61b5fd5e011912cd2083f2ec191cdf20'),('sandbox','42a63f7178748ea5dd4ce6964cbe9cd77d0131ca8dad795fbd61a4735b206706')]:
    raw=(ROOT / ('internal/fundinglotcatalog/expected/'+mode+'-v1.json')).read_bytes()
    d=wallet(json.loads(raw))
    assert sha(canonical(d))==root
    records=[]
    for variant in d['variants']:
        changes=transform(variant)
        records.append({'name':variant['name'],'changes':changes})
    delta['modes'][mode]={'base_sha256':root,'variants':records}
    summary[mode]={'base_sha256':root,'expected_sha256':sha(canonical(d)),'canonical_bytes':len(canonical(d)),'variants':{v['name']:{'schema':sha(canonical(v['schema'])),'roles':sha(canonical(v['roles']))} for v in d['variants']}}
output=ROOT/'internal/fundinglotcatalog/expected/periodic-prefix-v3-delta.json'
assert output.read_bytes()==canonical(delta), 'static PG16 derivation differs from reviewed record delta'
out=ROOT/'docs/verification/periodic-prefix-profile-roots.json'
assert json.loads(out.read_bytes())==summary, 'static PG16 derivation differs from reviewed roots'
print('static periodic-prefix record delta verified: SQL/ACL, original PG16 templates, 2 modes, 6 variants, no live input')
