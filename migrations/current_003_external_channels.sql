-- Phase 0 external upstream channel kinds and integration modes (035).
-- This is a current-only append migration. Historical migration plans never
-- execute this file.

-- channel_kind records which topology a channel version serves:
--   direct   - aigate's own provider client talks to the supplier endpoint;
--   new_api  - external New API gateway on an internal network;
--   sub2api  - external Sub2API gateway on an internal network.
ALTER TABLE channel_versions
  ADD COLUMN channel_kind text NOT NULL DEFAULT 'direct'
  CHECK (channel_kind IN ('direct','new_api','sub2api'));

-- integration_mode records how aigate executes the capability:
--   external_relay      - forward to the downstream gateway over the shared
--                         OpenAI-compatible contract;
--   embedded_adapter    - aigate's own provider adapter executes it;
--   migrated_capability - a capability previously relayed now runs inside
--                         aigate after an audited migration slice.
ALTER TABLE channel_versions
  ADD COLUMN integration_mode text NOT NULL DEFAULT 'embedded_adapter'
  CHECK (integration_mode IN ('external_relay','embedded_adapter','migrated_capability'));

-- A direct channel is by definition executed by aigate's own adapter; a
-- downstream gateway channel starts as an external relay and may later be
-- migrated in, but it can never be "relayed" to itself.
ALTER TABLE channel_versions
  ADD CONSTRAINT channel_versions_kind_mode_check
  CHECK ((channel_kind = 'direct' AND integration_mode <> 'external_relay')
      OR (channel_kind IN ('new_api','sub2api')));

-- The platform application writes channel versions once at creation; the
-- columns above follow the table's existing INSERT-only privilege posture.
