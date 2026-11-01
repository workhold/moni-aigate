package periodicbudget

const verifierVersionsSQL = `WITH versions AS (
 SELECT v.*,row_number() OVER(PARTITION BY control_id ORDER BY revision) n,
 lag(enabled) OVER(PARTITION BY control_id ORDER BY revision) prev_enabled,
 lag(limit_amount) OVER(PARTITION BY control_id ORDER BY revision) prev_limit
 FROM @versions@ v
), last_version AS (
 SELECT DISTINCT ON (control_id) * FROM @versions@ ORDER BY control_id,revision DESC
), configuration_bad AS (
 SELECT c.id FROM @controls@ c LEFT JOIN last_version v ON v.control_id=c.id
 LEFT JOIN @tenants@ t ON t.id=c.tenant_id LEFT JOIN @keys@ k ON k.id=c.key_id AND k.tenant_id=c.tenant_id
 LEFT JOIN @accounts@ a ON a.id=c.account_id
 WHERE v.control_id IS NULL OR
 (c.revision,c.period_kind,c.enabled,c.limit_amount,c.configuration_operation_id) IS DISTINCT FROM
 (v.revision,v.period_kind,v.enabled,v.limit_amount,v.operation_id)
 OR (c.scope='tenant' AND (t.id IS NULL OR c.key_id IS NOT NULL OR c.account_id IS NOT NULL))
 OR (c.scope='key' AND (t.id IS NULL OR k.id IS NULL OR c.account_id IS NOT NULL))
 OR (c.scope='procurement' AND (a.id IS NULL OR c.tenant_id IS NOT NULL OR c.key_id IS NOT NULL))
 OR c.scope NOT IN ('tenant','key','procurement') OR c.period_kind NOT IN ('utc_day','utc_month')
 OR EXISTS(SELECT 1 FROM @controls@ d WHERE d.id<>c.id AND d.scope=c.scope AND coalesce(d.account_id,d.key_id,d.tenant_id)=coalesce(c.account_id,c.key_id,c.tenant_id))
 OR c.limit_amount<0 OR c.revision<1 OR NOT isfinite(c.tracking_started_at) OR NOT isfinite(c.effective_from)
 OR c.effective_from IS DISTINCT FROM CASE c.period_kind
 WHEN 'utc_day' THEN (date_trunc('day',c.tracking_started_at AT TIME ZONE 'UTC')+interval '1 day') AT TIME ZONE 'UTC'
 WHEN 'utc_month' THEN (date_trunc('month',c.tracking_started_at AT TIME ZONE 'UTC')+interval '1 month') AT TIME ZONE 'UTC' END
), version_bad AS (
 SELECT v.control_id,v.revision FROM versions v LEFT JOIN @controls@ c ON c.id=v.control_id
 LEFT JOIN @operations@ o ON o.operation_id=v.operation_id LEFT JOIN @users@ u ON u.id=v.actor
 WHERE c.id IS NULL OR o.operation_id IS NULL OR u.id IS NULL OR v.revision<>v.n OR v.revision>c.revision
 OR v.period_kind IS DISTINCT FROM c.period_kind OR v.limit_amount<0
 OR (v.revision=1 AND v.created_xid IS DISTINCT FROM c.created_xid)
 OR (v.revision>1 AND (v.enabled,v.limit_amount) IS NOT DISTINCT FROM (v.prev_enabled,v.prev_limit))
 OR (o.resource_id,o.revision,o.actor,o.created_xid,o.created_at) IS DISTINCT FROM
    (v.control_id,v.revision,v.actor,v.created_xid,v.created_at)
)
SELECT (SELECT count(DISTINCT id) FROM configuration_bad),(SELECT count(*) FROM (SELECT DISTINCT control_id,revision FROM version_bad) bad)`

const verifierAuditsSQL = `WITH expected AS (
 SELECT o.operation_id,o.actor,o.created_at,c.id control_id,c.scope,c.tenant_id,
 jsonb_build_object('control_id',c.id,'revision',o.revision::text,'scope',c.scope,
  'scope_id',coalesce(c.account_id,c.key_id,c.tenant_id),'changed',v.operation_id IS NOT NULL) diff
 FROM @operations@ o LEFT JOIN @controls@ c ON c.id=o.resource_id
 LEFT JOIN @versions@ v ON v.operation_id=o.operation_id
), counts AS (
 SELECT target,count(*) n FROM @audits@ WHERE action='periodic_budget.configured' GROUP BY target
), bad AS (
 SELECT 'audit' kind,a.id pk FROM @audits@ a LEFT JOIN expected e ON e.operation_id=a.target
 LEFT JOIN counts n ON n.target=a.target
 WHERE a.action='periodic_budget.configured' AND
 (e.operation_id IS NULL OR e.control_id IS NULL OR n.n<>1 OR a.actor IS DISTINCT FROM e.actor
  OR a.tenant_scope IS DISTINCT FROM CASE WHEN e.scope='key' THEN e.tenant_id ELSE NULL END
  OR a.request_id IS DISTINCT FROM '' OR a.created_at IS DISTINCT FROM e.created_at OR a.sanitized_diff IS DISTINCT FROM e.diff)
 UNION SELECT 'missing',e.operation_id FROM expected e WHERE NOT EXISTS
 (SELECT 1 FROM @audits@ a WHERE a.action='periodic_budget.configured' AND a.target=e.operation_id)
)
SELECT count(*) FROM bad`
