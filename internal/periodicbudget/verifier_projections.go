package periodicbudget

const verifierProjectionsSQL = `WITH
allocation_events AS (
 SELECT control_id,allocation_id,count(*) n,sum(held_delta::numeric) held,sum(spent_delta::numeric) spent
 FROM @events@ GROUP BY control_id,allocation_id
), tails AS (
 SELECT DISTINCT ON (control_id,allocation_id) * FROM @events@ ORDER BY control_id,allocation_id,seq DESC,id DESC
), window_allocations AS (
 SELECT control_id,window_id,sum(held_amount::numeric) held,sum(spent_amount::numeric) spent
 FROM @allocations@ WHERE window_id IS NOT NULL GROUP BY control_id,window_id
), window_events AS (
 SELECT e.control_id,a.window_id,count(*) n,sum(e.held_delta::numeric) held,sum(e.spent_delta::numeric) spent,
 (array_agg(e.id ORDER BY e.seq DESC,e.id DESC))[1] last_event
 FROM @events@ e JOIN @allocations@ a ON a.control_id=e.control_id AND a.id=e.allocation_id
 WHERE a.window_id IS NOT NULL GROUP BY e.control_id,a.window_id
), control_allocations AS (
 SELECT control_id,sum(held_amount::numeric) held,sum(spent_amount::numeric) spent FROM @allocations@ GROUP BY control_id
), control_events AS (
 SELECT control_id,count(*) n,max(seq) max_seq,sum(held_delta::numeric) held,sum(spent_delta::numeric) spent,
 (array_agg(id ORDER BY seq DESC,id DESC))[1] last_event FROM @events@ GROUP BY control_id
), max_windows AS (SELECT control_id,max(start_at) start_at FROM @windows@ GROUP BY control_id),
allocation_bad AS (
 SELECT a.id FROM @allocations@ a
 LEFT JOIN allocation_events e ON e.control_id=a.control_id AND e.allocation_id=a.id
 LEFT JOIN tails t ON t.control_id=a.control_id AND t.allocation_id=a.id
 WHERE e.n IS NULL OR a.revision IS DISTINCT FROM e.n OR a.last_event_id IS DISTINCT FROM t.id
 OR (a.held_amount,a.spent_amount,a.source_held,a.source_spent,a.state) IS DISTINCT FROM
    (t.after_held,t.after_spent,t.after_held,t.after_source_spent,t.after_state)
 OR (a.held_amount,a.spent_amount) IS DISTINCT FROM (e.held,e.spent)
), window_bad AS (
 SELECT w.id FROM @windows@ w LEFT JOIN @controls@ c ON c.id=w.control_id
 LEFT JOIN window_allocations a ON a.control_id=w.control_id AND a.window_id=w.id
 LEFT JOIN window_events e ON e.control_id=w.control_id AND e.window_id=w.id
 WHERE c.id IS NULL OR NOT isfinite(w.start_at) OR NOT isfinite(w.end_at)
 OR w.held_amount<0 OR w.spent_amount<0 OR w.held_amount::text IN ('NaN','Infinity','-Infinity') OR w.spent_amount::text IN ('NaN','Infinity','-Infinity')
 OR w.start_at IS DISTINCT FROM CASE c.period_kind WHEN 'utc_day' THEN date_trunc('day',w.start_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' WHEN 'utc_month' THEN date_trunc('month',w.start_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' END
 OR w.end_at IS DISTINCT FROM CASE c.period_kind WHEN 'utc_day' THEN (w.start_at AT TIME ZONE 'UTC'+interval '1 day') AT TIME ZONE 'UTC' WHEN 'utc_month' THEN (w.start_at AT TIME ZONE 'UTC'+interval '1 month') AT TIME ZONE 'UTC' END
 OR (w.held_amount,w.spent_amount) IS DISTINCT FROM (coalesce(a.held,0),coalesce(a.spent,0))
 OR (w.held_amount,w.spent_amount) IS DISTINCT FROM (coalesce(e.held,0),coalesce(e.spent,0))
 OR w.revision IS DISTINCT FROM coalesce(e.n,0) OR w.last_event_id IS DISTINCT FROM e.last_event
), control_bad AS (
 SELECT c.id FROM @controls@ c LEFT JOIN control_allocations a ON a.control_id=c.id
 LEFT JOIN control_events e ON e.control_id=c.id LEFT JOIN max_windows w ON w.control_id=c.id
 WHERE (c.held_amount,c.spent_amount) IS DISTINCT FROM (coalesce(a.held,0),coalesce(a.spent,0))
 OR (c.held_amount,c.spent_amount) IS DISTINCT FROM (coalesce(e.held,0),coalesce(e.spent,0))
 OR c.event_seq IS DISTINCT FROM coalesce(e.n,0) OR c.event_seq IS DISTINCT FROM coalesce(e.max_seq,0)
 OR c.last_event_id IS DISTINCT FROM e.last_event OR NOT isfinite(c.max_observed_window_start)
 OR c.held_amount<0 OR c.spent_amount<0 OR c.held_amount::text IN ('NaN','Infinity','-Infinity') OR c.spent_amount::text IN ('NaN','Infinity','-Infinity')
 OR c.max_observed_window_start IS DISTINCT FROM greatest(w.start_at,
  CASE c.period_kind WHEN 'utc_day' THEN date_trunc('day',c.tracking_started_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
   WHEN 'utc_month' THEN date_trunc('month',c.tracking_started_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' END)
)
SELECT (SELECT count(DISTINCT id) FROM allocation_bad),(SELECT count(DISTINCT id) FROM window_bad),(SELECT count(DISTINCT id) FROM control_bad)`

const verifierProcurementSQL = `WITH source_sums AS (
 SELECT attempt_id,account_id,sum(held_delta::numeric) held,sum(spent_delta::numeric) spent,
 count(*) FILTER(WHERE type='reserved') reserved_count,
 count(*) FILTER(WHERE type='reserved' AND event_key='reserve:'||attempt_id) canonical_count,
 bool_or(held_delta::text IN ('NaN','Infinity','-Infinity') OR spent_delta::text IN ('NaN','Infinity','-Infinity')) invalid_amount,
 bool_or(type NOT IN ('reserved','consumption_unknown','recovered_unknown','released_no_consumption','expired_without_permit','cost_confirmed','cost_corrected','evidence_disputed')) invalid_type
 FROM @pe@ GROUP BY attempt_id,account_id
), account_sources AS (
 SELECT account_id,sum(held_amount::numeric) held,sum(spent_amount::numeric) spent FROM @procurement@ GROUP BY account_id
), account_events AS (
 SELECT account_id,sum(held_delta::numeric) held,sum(spent_delta::numeric) spent FROM @pe@ GROUP BY account_id
), procurement_bad AS (
 SELECT 'supplier' kind,p.attempt_id pk FROM @procurement@ p LEFT JOIN source_sums s ON s.attempt_id=p.attempt_id AND s.account_id=p.account_id
 WHERE (p.held_amount,p.spent_amount) IS DISTINCT FROM (coalesce(s.held,0),coalesce(s.spent,0))
 OR p.held_amount<0 OR p.spent_amount<0 OR p.held_amount::text IN ('NaN','Infinity','-Infinity') OR p.spent_amount::text IN ('NaN','Infinity','-Infinity') OR s.invalid_amount OR s.invalid_type
 OR s.reserved_count IS DISTINCT FROM 1::bigint OR s.canonical_count IS DISTINCT FROM 1::bigint
 OR NOT EXISTS(SELECT 1 FROM @pe@ e WHERE e.attempt_id=p.attempt_id AND e.account_id=p.account_id
  AND (e.type,e.event_key,e.held_delta,e.spent_delta)=('reserved','reserve:'||p.attempt_id,p.upper_amount::numeric,0::numeric))
 UNION SELECT 'account',q.id FROM @accounts@ q
 LEFT JOIN account_sources s ON s.account_id=q.id LEFT JOIN account_events e ON e.account_id=q.id
 WHERE (q.held_amount,q.spent_amount) IS DISTINCT FROM (coalesce(s.held,0),coalesce(s.spent,0))
 OR q.held_amount<0 OR q.spent_amount<0 OR q.held_amount::text IN ('NaN','Infinity','-Infinity') OR q.spent_amount::text IN ('NaN','Infinity','-Infinity')
 OR (q.held_amount,q.spent_amount) IS DISTINCT FROM (coalesce(e.held,0),coalesce(e.spent,0))
), first_event AS (
 SELECT DISTINCT ON (allocation_id) * FROM @events@ ORDER BY allocation_id,seq,id
), residual AS (
 SELECT a.id,count(p.id) n,coalesce(sum(p.held_delta::numeric),0) held,coalesce(sum(p.spent_delta::numeric),0) spent
 FROM @allocations@ a JOIN @controls@ c ON c.id=a.control_id AND c.scope='procurement'
 LEFT JOIN @pe@ p ON p.attempt_id=a.subject_id AND p.account_id=c.account_id
 AND NOT EXISTS(SELECT 1 FROM @events@ e WHERE e.control_id=a.control_id AND e.allocation_id=a.id AND e.source_kind='procurement_event' AND e.source_id=p.id)
 WHERE a.subject_type='supplier_attempt' GROUP BY a.id
), baseline_bad AS (
 SELECT a.id FROM @allocations@ a LEFT JOIN first_event f ON f.allocation_id=a.id
 LEFT JOIN residual r ON r.id=a.id LEFT JOIN @pe@ p ON p.id=a.origin_source_id
 WHERE a.subject_type='supplier_attempt' AND (
  (a.origin='new' AND (a.baseline<>0 OR r.n IS DISTINCT FROM 0::bigint))
  OR (a.origin='legacy_import' AND (f.id IS NULL OR (r.held,r.spent) IS DISTINCT FROM (f.after_held,a.baseline)
    OR f.after_source_spent IS DISTINCT FROM a.baseline OR f.after_spent IS DISTINCT FROM 0::numeric))
  OR (a.origin='legacy_catchup' AND (f.id IS NULL OR p.id IS NULL
    OR (r.held,r.spent) IS DISTINCT FROM (0::numeric,a.baseline)
    OR f.after_held-p.held_delta IS DISTINCT FROM 0::numeric
    OR f.after_source_spent-p.spent_delta IS DISTINCT FROM a.baseline
    OR f.after_spent IS DISTINCT FROM greatest(f.after_source_spent-a.baseline,0::numeric))))
)
SELECT (SELECT count(*) FROM procurement_bad),(SELECT count(DISTINCT id) FROM baseline_bad),
 (SELECT count(DISTINCT a.id) FROM residual r JOIN @allocations@ a ON a.id=r.id WHERE a.origin IN ('legacy_import','legacy_catchup') AND r.n>0)`
