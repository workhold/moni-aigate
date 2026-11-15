-- 034.6 independent, append-only console display configuration.
-- This table is never a coin accounting, metering, reservation or refund rate source.

CREATE TABLE coin_display_versions (
 operation_id text PRIMARY KEY,
 version bigint NOT NULL UNIQUE CHECK(version>0),
 display_name text NOT NULL CHECK(octet_length(display_name) BETWEEN 1 AND 32 AND btrim(display_name)=display_name AND display_name!~'[[:cntrl:]]'),
 fiat_currency text,
 rate_num bigint,
 rate_den bigint,
 actor text REFERENCES users(id),
 reason text NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 200 AND btrim(reason)=reason AND reason!~'[[:cntrl:]]'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CONSTRAINT coin_display_rate_check CHECK ((fiat_currency IS NULL AND rate_num IS NULL AND rate_den IS NULL) OR (fiat_currency IN ('CNY','USD') AND rate_num>0 AND rate_den>0)),
 CONSTRAINT coin_display_actor_check CHECK ((version=1 AND actor IS NULL) OR (version>1 AND actor IS NOT NULL))
);
INSERT INTO coin_display_versions(operation_id,version,display_name,fiat_currency,rate_num,rate_den,actor,reason)
VALUES(gen_random_uuid()::text,1,'币',NULL,NULL,NULL,NULL,'034.6 display genesis');
CREATE TRIGGER immutable_coin_display_version BEFORE UPDATE OR DELETE ON coin_display_versions FOR EACH ROW EXECUTE FUNCTION reject_history_mutation();
