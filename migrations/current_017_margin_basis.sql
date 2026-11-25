ALTER TABLE route_decisions DROP CONSTRAINT route_decisions_algorithm_version_check;
ALTER TABLE route_decisions ADD CONSTRAINT route_decisions_algorithm_version_check CHECK (algorithm_version IN ('legacy-v1','dynamic-v1','dynamic-v2'));
