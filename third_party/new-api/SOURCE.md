# New API source and modification record

- Upstream: https://github.com/QuantumNous/new-api
- Exact source commit: `e449cc700eb569cdfc2199cb73e97de46944f43c`
- Integration mode: `external_relay` in `deploy/external-upstreams.compose.yaml`; local image `local/new-api:e449cc7` built from that commit for linux/arm64.
- License: GNU Affero General Public License v3.0; see `LICENSE`, `NOTICE`, and `THIRD-PARTY-LICENSES.md` retrieved from the same commit.
- Local upstream source modifications: **none**. No upstream file is patched or vendored as modified code in this repository.
- Local integration changes are outside the upstream tree: Compose environment/network configuration, aigate channel bindings, and `scripts/external-upstream-qa.py` fixture orchestration only. These changes do not alter New API's database, customer wallet, routing authority, or settlement behavior.
- Attribution and source offer: retain the upstream project link and contributor notices in distributed images/documentation. Exact source for the executed image is available from the commit URL above; `NOTICE` and the full third-party notice files accompany this record.
- Verification: `docs/verification/phase0-external-upstream.md` and `docs/verification/phase0-upstream-provenance.md`. The deterministic supplier fixture is not a New API supplier or production certification.
