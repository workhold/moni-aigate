package alerts

const channelSourceSQL = `-- channel；LEFT JOIN即使源缺失也返回一行，NULL不当false。
WITH observed AS MATERIALIZED (SELECT clock_timestamp()::timestamptz(6) AS as_of)
SELECT (c.id IS NOT NULL) AS source_exists,o.as_of,c.enabled,c.quarantined
FROM observed o LEFT JOIN channels c ON c.id=$1;`

const budgetSourceSQL = `-- 019登记采购预算；可负available，非供应商账户现金观察。
WITH observed AS MATERIALIZED (SELECT clock_timestamp()::timestamptz(6) AS as_of)
SELECT (a.id IS NOT NULL) AS source_exists,o.as_of,a.state,a.limit_amount::text,
 a.held_amount::text,a.spent_amount::text,
 (a.limit_amount::numeric-a.held_amount-a.spent_amount)::text AS available_amount
FROM observed o LEFT JOIN procurement_accounts a ON a.id=$1;`

const customerSourceSQL = `-- 客户未决创建年龄；$2为已经验证的600..604800秒整数，不是SQL片段。
WITH observed AS MATERIALIZED (SELECT clock_timestamp()::timestamptz(6) AS as_of),
eligible AS MATERIALIZED (
 SELECT r.created_at,r.request_id AS reference_id
 FROM reservations r
 WHERE r.tenant_id=$1 AND r.status='pending_reconciliation'
  AND r.created_at<=(SELECT as_of FROM observed)-$2::bigint*interval '1 second'
 ORDER BY r.created_at ASC,r.request_id COLLATE "C" ASC LIMIT 1001
), measured AS (
 SELECT count(*) AS observed_count FROM eligible
), oldest AS (
 SELECT created_at,reference_id FROM eligible
 ORDER BY created_at ASC,reference_id COLLATE "C" ASC LIMIT 1
)
SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1) AS source_exists,o.as_of,
 m.observed_count::text,(m.observed_count=1001) AS count_is_lower_bound,
 oldest.created_at,oldest.reference_id
FROM observed o CROSS JOIN measured m LEFT JOIN oldest ON true;`

const procurementSourceSQL = `-- 采购未决创建年龄；独立于客户24h释放。
WITH observed AS MATERIALIZED (SELECT clock_timestamp()::timestamptz(6) AS as_of),
eligible AS MATERIALIZED (
 SELECT r.created_at,r.attempt_id AS reference_id
 FROM procurement_reservations r
 WHERE r.account_id=$1 AND r.state IN ('unknown','disputed')
  AND r.created_at<=(SELECT as_of FROM observed)-$2::bigint*interval '1 second'
 ORDER BY r.created_at ASC,r.attempt_id COLLATE "C" ASC LIMIT 1001
), measured AS (
 SELECT count(*) AS observed_count FROM eligible
), oldest AS (
 SELECT created_at,reference_id FROM eligible
 ORDER BY created_at ASC,reference_id COLLATE "C" ASC LIMIT 1
)
SELECT EXISTS(SELECT 1 FROM procurement_accounts WHERE id=$1) AS source_exists,o.as_of,
 m.observed_count::text,(m.observed_count=1001) AS count_is_lower_bound,
 oldest.created_at,oldest.reference_id
FROM observed o CROSS JOIN measured m LEFT JOIN oldest ON true;`
