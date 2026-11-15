# Sub2API source and modification record

- Upstream: https://github.com/Wei-Shaw/sub2api
- Exact source commit: `10ba8f9b4dc2e6023d4a9879ab77b21841babd10`
- Executed image: `weishaw/sub2api@sha256:868c15f3c729af05896c94b3456d91a68bc81dce50c4d7b4557682df7ca92c13` (v0.2.21 lineage).
- Integration mode: `external_relay` in `deploy/external-upstreams.compose.yaml`; the official image is used, with account, group, Key and QA balance created through its management API.
- License: GNU Lesser General Public License v3.0; see `LICENSE` retrieved from the exact commit. No separate upstream NOTICE file exists at that commit.
- Local upstream source modifications: **none**. No upstream file is patched or vendored as modified code in this repository.
- Local integration changes are outside the upstream tree: Compose environment/network configuration, aigate channel bindings, and `scripts/external-upstream-qa.py` fixture orchestration only. These changes do not alter Sub2API's database, customer wallet, routing authority, or settlement behavior.
- Source offer and attribution: exact source corresponding to the image lineage is linked by the commit above, and the image digest is fixed for repeatable retrieval. The LICENSE accompanies this record.
- Verification: `docs/verification/phase0-external-upstream.md` and `docs/verification/phase0-upstream-provenance.md`. The deterministic supplier fixture is not a Sub2API supplier or production certification.
