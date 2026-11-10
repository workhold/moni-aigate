#!/usr/bin/env bash
set -Eeuo pipefail

# Current MVP acceptance entry point.
#
# This is deliberately an explicit test inventory. It does not invoke
# `go test ./...`, because that command includes historical maintenance and
# compatibility fixtures which are not part of the current-only MVP gate.
# Every database group is required to use the disposable dedicated PostgreSQL
# instance on 127.0.0.1:55439.

readonly expected_database_url='postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable'
readonly repo_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
readonly default_cache_dir=/tmp/moni-aigate-gocache-current-mvp

cd "$repo_dir"

export PATH="/opt/homebrew/bin:$PATH"
export TEST_DATABASE_URL="${TEST_DATABASE_URL:-$expected_database_url}"
if [ "$TEST_DATABASE_URL" != "$expected_database_url" ]; then
  cat >&2 <<EOF
verify-current-mvp requires the dedicated current-MVP database:
  $expected_database_url
received:
  $TEST_DATABASE_URL
This entry point never falls back to 5432 or an arbitrary database.
EOF
  exit 2
fi

export GOCACHE="${GOCACHE:-$default_cache_dir}"
mkdir -p "$GOCACHE"

log_dir=${VERIFY_CURRENT_MVP_LOG_DIR:-}
remove_logs=true
if [ -z "$log_dir" ]; then
  log_dir=$(mktemp -d "${TMPDIR:-/tmp}/moni-aigate-current-mvp.XXXXXX")
else
  mkdir -p "$log_dir"
  remove_logs=false
fi

declare -a result_names=()
declare -a result_statuses=()
declare -a result_seconds=()
failed=0
skipped=0

slugify() {
  printf '%s' "$1" | tr '[:upper:] ' '[:lower:]_' | tr -cs '[:alnum:]_-' '_'
}

run_group() {
  local name=$1
  shift
  local slug start elapsed status log
  slug=$(slugify "$name")
  log="$log_dir/$slug.log"
  start=$SECONDS
  printf '\n===== %s =====\n' "$name"
  printf 'command:'
  printf ' %q' "$@"
  printf '\n'
  set +e
  "$@" 2>&1 | tee "$log"
  status=${PIPESTATUS[0]}
  set -e
  elapsed=$((SECONDS - start))
  result_names+=("$name")
  result_seconds+=("$elapsed")
  if [ "$status" -eq 0 ]; then
    result_statuses+=(PASS)
    printf '%s\n' "----- $name: PASS (${elapsed}s) -----"
  else
    result_statuses+=(FAIL)
    failed=1
    printf '%s\n' "----- $name: FAIL (${elapsed}s; log=$log) -----" >&2
  fi
}

skip_group() {
  local name=$1
  local reason=$2
  result_names+=("$name")
  result_statuses+=(SKIP)
  result_seconds+=(0)
  skipped=$((skipped + 1))
  printf '\n===== %s: SKIP =====\n%s\n' "$name" "$reason"
}

go_race_test() {
  go test -race -p 1 -count=1 "$@"
}

# A go test invocation with a non-matching -run expression exits successfully
# with "no tests to run". That is unsafe for an acceptance gate, so every
# explicitly named test is checked against the package test inventory first.
# Specs are passed as package:test-name pairs, followed by `--` and the
# packages that the real command must execute.
checked_go_race_test() {
  local timeout=$1
  local run_pattern=$2
  shift 2
  local -a specs=()
  while [ "$#" -gt 0 ] && [ "$1" != '--' ]; do
    specs+=("$1")
    shift
  done
  [ "$#" -gt 0 ] || { echo 'checked_go_race_test requires -- and package list' >&2; return 2; }
  shift
  local -a packages=("$@")
  [ "${#specs[@]}" -gt 0 ] || { echo 'checked_go_race_test requires at least one test name' >&2; return 2; }
  [ "${#packages[@]}" -gt 0 ] || { echo 'checked_go_race_test requires at least one package' >&2; return 2; }

  local spec package test_name inventory
  for spec in "${specs[@]}"; do
    case "$spec" in
      *:*) package=${spec%%:*}; test_name=${spec#*:} ;;
      *) echo "invalid test inventory entry: $spec" >&2; return 2 ;;
    esac
    inventory=$(go test -run '^$' -list "^${test_name}$" "$package" 2>&1) || {
      echo "could not inspect test inventory for $package:$test_name" >&2
      printf '%s\n' "$inventory" >&2
      return 1
    }
    if ! grep -Fxq "$test_name" <<<"$inventory"; then
      echo "test name is absent from $package: $test_name" >&2
      return 1
    fi
  done
  go_race_test "${packages[@]}" -timeout "$timeout" -run "$run_pattern"
}

check_provider_timeout_wiring() {
  local failed_wiring=0
  local timeout_fields='ConnectTimeout|HeaderTimeout|FirstEventTimeout|IdleTimeout|ProgressTimeout'
  if ! rg -q "$timeout_fields" internal/config; then
    echo 'provider timeout fields are absent from internal/config'
    failed_wiring=1
  fi
  if ! rg -q 'ProviderTimeouts|ClientOptions\(\)' internal/server cmd/moni-aigate; then
    echo 'provider timeout fields are not consumed by server startup'
    failed_wiring=1
  fi
  if rg -q 'provider\.NewClient\(provider\.ClientOptions\{\}\)' internal/server cmd/moni-aigate; then
    echo 'server still constructs provider.NewClient with an empty ClientOptions value'
    failed_wiring=1
  fi
  if [ "$failed_wiring" -ne 0 ]; then
    echo 'Config -> Server provider timeout wiring is not complete' >&2
    return 1
  fi
  echo 'Config -> Server provider timeout wiring is present'
}

run_group 'static current architecture checks' \
  go run ./scripts/check-architecture

run_group 'static current procurement layout check' \
  go run ./scripts/check-procurement-layout

run_group 'current-only migration VerifyCurrent and append path' \
  checked_go_race_test 300s \
    '^(TestCurrentInstallerRequiresEmptyDatabaseAndVerifiesCompleteBaseline|TestEmbeddedPrefixIsExactClosedSet)$' \
    ./migrations:TestCurrentInstallerRequiresEmptyDatabaseAndVerifiesCompleteBaseline \
    ./migrations:TestEmbeddedPrefixIsExactClosedSet \
    -- ./migrations

run_group 'current permissions and legacy/refusal boundaries' \
  checked_go_race_test 600s \
    '^(TestUnifiedTextMeteringRejectsEveryListedLegacyRelation|TestUnifiedTextMeteringRejectsChanged023DependencyBeforeDDL|TestUnifiedTextMeteringRejectsChanged023ACLBeforeDDL|TestUnifiedTextMeteringRejectsZeroBalanceBusinessHistory|TestMeterRoleBoundaryWithActualLoginConnections|TestWalletSourcesRolesActualCurrentMatrix|TestWalletSourcesRolesDMLAndWorkerReadBoundary|TestWalletSourcesRolesDetectPrivilegeAndFunctionDrift|TestPaymentRolesImplicitDatabaseOwnerSchema|TestPaymentRolesEmptyOffActualLoginsAndRepeatedProvision)$' \
    ./migrations:TestUnifiedTextMeteringRejectsEveryListedLegacyRelation \
    ./migrations:TestUnifiedTextMeteringRejectsChanged023DependencyBeforeDDL \
    ./migrations:TestUnifiedTextMeteringRejectsChanged023ACLBeforeDDL \
    ./migrations:TestUnifiedTextMeteringRejectsZeroBalanceBusinessHistory \
    ./internal/operations:TestMeterRoleBoundaryWithActualLoginConnections \
    ./internal/operations:TestWalletSourcesRolesActualCurrentMatrix \
    ./internal/operations:TestWalletSourcesRolesDMLAndWorkerReadBoundary \
    ./internal/operations:TestWalletSourcesRolesDetectPrivilegeAndFunctionDrift \
    ./internal/operations:TestPaymentRolesImplicitDatabaseOwnerSchema \
    ./internal/operations:TestPaymentRolesEmptyOffActualLoginsAndRepeatedProvision \
    -- ./migrations ./internal/operations

run_group '028-U PostgreSQL guard matrix' \
  checked_go_race_test 900s \
    '^Test(UnifiedTextMeteringCanonical|MeterCompletenessRejectsGaps|UnifiedTextMeteringGuardMatrix|UnifiedTextMeteringGuardRoleMatrix|UnifiedTextMeteringAmountAndSettlementGuards|UnifiedTextMeteringSchemaAndCapabilityCompletenessGuards|UnifiedTextMeteringFinalizationPointerAndEventIdentityGuards|UnifiedTextMeteringConcurrentCorrectionIdentityIsUnique|UnifiedTextMeteringCurrentShapeHasNoScalarProjection|UnifiedTextMeteringCurrentGuardFunctionsAreInvokerAndPinned|UnifiedTextMeteringAttemptQuoteBindingAndSendingGuards|UnifiedTextMeteringSnapshotTenantBindingRollsBack|UnifiedTextMeteringSupplierCorrectionPointerGuard|UnifiedTextMeteringDoesNotRetainRouteRevisionBypass|UnifiedTextMeteringGuardClosureDeferredAggregateRollback|UnifiedTextMeteringGuardClosureRuntimeCrossRequestAttemptRollsBack|UnifiedTextMeteringGuardClosureSourceIdentityTenantScopeAndConflictRace|UnifiedTextMeteringGuardClosureActualLoginFunctionSurface)$' \
    ./migrations:TestUnifiedTextMeteringCanonical \
    ./migrations:TestMeterCompletenessRejectsGaps \
    ./migrations:TestUnifiedTextMeteringGuardMatrix \
    ./migrations:TestUnifiedTextMeteringGuardRoleMatrix \
    ./migrations:TestUnifiedTextMeteringAmountAndSettlementGuards \
    ./migrations:TestUnifiedTextMeteringSchemaAndCapabilityCompletenessGuards \
    ./migrations:TestUnifiedTextMeteringFinalizationPointerAndEventIdentityGuards \
    ./migrations:TestUnifiedTextMeteringConcurrentCorrectionIdentityIsUnique \
    ./migrations:TestUnifiedTextMeteringCurrentShapeHasNoScalarProjection \
    ./migrations:TestUnifiedTextMeteringCurrentGuardFunctionsAreInvokerAndPinned \
    ./migrations:TestUnifiedTextMeteringAttemptQuoteBindingAndSendingGuards \
    ./migrations:TestUnifiedTextMeteringSnapshotTenantBindingRollsBack \
    ./migrations:TestUnifiedTextMeteringSupplierCorrectionPointerGuard \
    ./migrations:TestUnifiedTextMeteringDoesNotRetainRouteRevisionBypass \
    ./migrations:TestUnifiedTextMeteringGuardClosureDeferredAggregateRollback \
    ./migrations:TestUnifiedTextMeteringGuardClosureRuntimeCrossRequestAttemptRollsBack \
    ./migrations:TestUnifiedTextMeteringGuardClosureSourceIdentityTenantScopeAndConflictRace \
    ./migrations:TestUnifiedTextMeteringGuardClosureActualLoginFunctionSurface \
    -- ./migrations

run_group 'wallet funding billing and settlement' \
  checked_go_race_test 1200s \
    '^(TestPostgresLifecycle|TestPostgresEvidenceAndReconciliation|TestPostgresSettlementPolicy|TestPostgresFundingManualConcurrentApprovalHasOneReceiptAndCreditedEvent|TestPostgresFundingManualAuditAndOutboxFailuresRollbackAllFacts|TestPostgresWalletOpeningOneNoncashSourceAndIntentReplay|TestPostgresWalletOpeningAuditFailureRollsBackSource|TestPostgresWalletOpeningUnknownCommitDoesNotPublishOrRepeat|TestPostgresWalletSourcesRuntimeFIFOFinishAndReverseRefund|TestPostgresWalletSourcesRuntimeEventFailureRollsBack|TestPostgresMediaReserveFinalizeUsesWalletLotsAndIsIdempotent|TestPostgresMediaUnknownFinalizationReleasesWithoutCharge|TestHTTPCommerceIssuanceDualApprovalUniqueFundingAndOffReadback|TestWalletSourcesHTTPCommercialFlowAndAuthorization)$' \
    ./internal/billing:TestPostgresLifecycle \
    ./internal/billing:TestPostgresEvidenceAndReconciliation \
    ./internal/billing:TestPostgresSettlementPolicy \
    ./internal/billing:TestPostgresFundingManualConcurrentApprovalHasOneReceiptAndCreditedEvent \
    ./internal/billing:TestPostgresFundingManualAuditAndOutboxFailuresRollbackAllFacts \
    ./internal/billing:TestPostgresWalletOpeningOneNoncashSourceAndIntentReplay \
    ./internal/billing:TestPostgresWalletOpeningAuditFailureRollsBackSource \
    ./internal/billing:TestPostgresWalletOpeningUnknownCommitDoesNotPublishOrRepeat \
    ./internal/billing:TestPostgresWalletSourcesRuntimeFIFOFinishAndReverseRefund \
    ./internal/billing:TestPostgresWalletSourcesRuntimeEventFailureRollsBack \
    ./internal/billing:TestPostgresMediaReserveFinalizeUsesWalletLotsAndIsIdempotent \
    ./internal/billing:TestPostgresMediaUnknownFinalizationReleasesWithoutCharge \
    ./internal/server:TestHTTPCommerceIssuanceDualApprovalUniqueFundingAndOffReadback \
    ./internal/server:TestWalletSourcesHTTPCommercialFlowAndAuthorization \
    -- ./internal/billing ./internal/server

run_group 'provider usage and current error/timeout handling' \
  checked_go_race_test 600s \
    '^Test(OpenAICompatibleDeepSeekUsageAcrossHTTPModes|DeepSeekCacheUsageNormalizesOnlyCompleteCacheEvidence|NativeSSEFragmentationFinalEventsAndCacheAccounting|NativeUsageFailuresRemainUnbillable|HeaderAndFirstEventTimeout|RoleHeartbeatCannotExtendProgressOrTotalDeadline)$' \
    ./internal/provider:TestOpenAICompatibleDeepSeekUsageAcrossHTTPModes \
    ./internal/provider:TestDeepSeekCacheUsageNormalizesOnlyCompleteCacheEvidence \
    ./internal/provider:TestNativeSSEFragmentationFinalEventsAndCacheAccounting \
    ./internal/provider:TestNativeUsageFailuresRemainUnbillable \
    ./internal/provider:TestHeaderAndFirstEventTimeout \
    ./internal/provider:TestRoleHeartbeatCannotExtendProgressOrTotalDeadline \
    -- ./internal/provider

run_group 'provider config catalog server race compile' \
  go_race_test ./internal/provider ./internal/config ./internal/catalog ./internal/server \
    -timeout 600s -run '^$'

run_group 'config and catalog current contract race' \
  checked_go_race_test 900s \
    '^Test(OperationsAlertsDefaultOffAndInvalidConfiguration|MFAEnrollmentPolicyConfiguration|DisabledMailDropsUnusedCredentials|MailConfigurationBoundaries|PaymentsModeFailsClosedBeforeConnection|TrustedProxyCIDRsAreExplicitAndBounded|TrustedProxyConfigurationLoadRejectsInvalidValues|VoucherConfigurationDefaultsAndIndependentStrictKeys|GroupPriceVectorApplicationReplayAndConcurrency|GroupPriceVectorFailuresRollback|GroupPriceVectorHistoryPagingAndZero|GroupPriceVectorRawCommitRejectsGaps|GroupPriceVectorFingerprintGolden|GroupPriceVectorResultRejectsScalarAndInvalidRates|TenantPricingCommandsAndOriginalReceipts|TenantPricingConcurrentOperationAndAuditRollback|TenantPricingReadIsolationPreviewAndPagination)$' \
    ./internal/config:TestOperationsAlertsDefaultOffAndInvalidConfiguration \
    ./internal/config:TestMFAEnrollmentPolicyConfiguration \
    ./internal/config:TestDisabledMailDropsUnusedCredentials \
    ./internal/config:TestMailConfigurationBoundaries \
    ./internal/config:TestPaymentsModeFailsClosedBeforeConnection \
    ./internal/config:TestTrustedProxyCIDRsAreExplicitAndBounded \
    ./internal/config:TestTrustedProxyConfigurationLoadRejectsInvalidValues \
    ./internal/config:TestVoucherConfigurationDefaultsAndIndependentStrictKeys \
    ./internal/catalog:TestGroupPriceVectorApplicationReplayAndConcurrency \
    ./internal/catalog:TestGroupPriceVectorFailuresRollback \
    ./internal/catalog:TestGroupPriceVectorHistoryPagingAndZero \
    ./internal/catalog:TestGroupPriceVectorRawCommitRejectsGaps \
    ./internal/catalog:TestGroupPriceVectorFingerprintGolden \
    ./internal/catalog:TestGroupPriceVectorResultRejectsScalarAndInvalidRates \
    ./internal/catalog:TestTenantPricingCommandsAndOriginalReceipts \
    ./internal/catalog:TestTenantPricingConcurrentOperationAndAuditRollback \
    ./internal/catalog:TestTenantPricingReadIsolationPreviewAndPagination \
    -- ./internal/config ./internal/catalog

run_group 'Config to Server provider timeout wiring audit' \
  check_provider_timeout_wiring

run_group 'six-text commercial path' \
  checked_go_race_test 1800s \
    '^TestCostVectorApplication(QuoteChain|UnknownIsNotFree|RecoveryDoesNotRegenerate|IdempotencyChargesOnceAcrossProtocols|DefaultAndGroupPricesAcrossProtocols)$' \
    ./internal/server:TestCostVectorApplicationQuoteChain \
    ./internal/server:TestCostVectorApplicationUnknownIsNotFree \
    ./internal/server:TestCostVectorApplicationRecoveryDoesNotRegenerate \
    ./internal/server:TestCostVectorApplicationIdempotencyChargesOnceAcrossProtocols \
    ./internal/server:TestCostVectorApplicationDefaultAndGroupPricesAcrossProtocols \
    -- ./internal/server

run_group 'image TTS async-video and Realtime current PostgreSQL path' \
  checked_go_race_test 1800s \
    '^(TestImageGenerationHTTPUsesCurrentMediaWalletFacts|TestSpeechHTTPUsesCurrentMediaWalletFacts|TestMediaResultsSurviveProcessCacheReset|TestVideoTaskUsesCurrentMediaWalletAcrossSubmitAndTerminalObservation|TestVideoTaskArtifactSurvivesStoreAndProviderRestart|TestVideoTaskObservationSurvivesWorkerProviderRestart|TestVideoTaskFailureAndCancelReleaseMedia|TestVideoTaskUnknownSubmissionReleasesMediaAndPreventsResubmit|TestRealtimeHTTPPostgresOneSessionOneTurn|TestPostgresRealtimePersistenceLifecycleAndTenantIsolation|TestPostgresRealtimeServerSelectsDatabasePersistence|TestMVPPostgresActualMainAlertsDefaultOffAndCrashRecovery|TestMVPPostgresActualMainSIGTERMDrainsSSEAndRestartDoesNotReplay)$' \
    ./internal/server:TestImageGenerationHTTPUsesCurrentMediaWalletFacts \
    ./internal/server:TestSpeechHTTPUsesCurrentMediaWalletFacts \
    ./internal/server:TestMediaResultsSurviveProcessCacheReset \
    ./internal/server:TestVideoTaskUsesCurrentMediaWalletAcrossSubmitAndTerminalObservation \
    ./internal/server:TestVideoTaskArtifactSurvivesStoreAndProviderRestart \
    ./internal/server:TestVideoTaskObservationSurvivesWorkerProviderRestart \
    ./internal/server:TestVideoTaskFailureAndCancelReleaseMedia \
    ./internal/server:TestVideoTaskUnknownSubmissionReleasesMediaAndPreventsResubmit \
    ./internal/server:TestRealtimeHTTPPostgresOneSessionOneTurn \
    ./internal/server:TestPostgresRealtimePersistenceLifecycleAndTenantIsolation \
    ./internal/server:TestPostgresRealtimeServerSelectsDatabasePersistence \
    ./internal/server:TestMVPPostgresActualMainAlertsDefaultOffAndCrashRecovery \
    ./internal/server:TestMVPPostgresActualMainSIGTERMDrainsSSEAndRestartDoesNotReplay \
    -- ./internal/server

run_group 'media state-machine and adapter safety tests' \
  checked_go_race_test 600s \
    '^(TestLifecycleNeverInfersConsumptionFromResult|TestLifecyclePostDispatchFailureIsUnknown|TestOrchestratorFullLifecycleAndRefs|TestOrchestratorUnknownIsTerminalAndNeverResubmits|TestOrchestratorCancellationIsSingleUse|TestMockRealtimeAdapterCompletesOneResponse|TestMockRealtimeAdapterUnknownHandleIsAnError|TestImageGenerationStrictInputAndInlineResult|TestImageGenerationIdempotencyDoesNotRegenerate|TestSynthesizeSpeechReturnsWAVAndReplaysWithoutProviderCall|TestSynthesizeSpeechUnknownSubmissionCannotRetry)$' \
    ./internal/media:TestLifecycleNeverInfersConsumptionFromResult \
    ./internal/media:TestLifecyclePostDispatchFailureIsUnknown \
    ./internal/media:TestOrchestratorFullLifecycleAndRefs \
    ./internal/media:TestOrchestratorUnknownIsTerminalAndNeverResubmits \
    ./internal/media:TestOrchestratorCancellationIsSingleUse \
    ./internal/provider:TestMockRealtimeAdapterCompletesOneResponse \
    ./internal/provider:TestMockRealtimeAdapterUnknownHandleIsAnError \
    ./internal/server:TestImageGenerationStrictInputAndInlineResult \
    ./internal/server:TestImageGenerationIdempotencyDoesNotRegenerate \
    ./internal/server:TestSynthesizeSpeechReturnsWAVAndReplaysWithoutProviderCall \
    ./internal/server:TestSynthesizeSpeechUnknownSubmissionCannotRetry \
    -- ./internal/media ./internal/provider ./internal/server

run_group 'current Go vet' \
  go vet ./migrations ./internal/billing ./internal/catalog ./internal/operations ./internal/provider ./internal/media ./internal/server

if [ ! -x web/node_modules/.bin/vue-tsc ]; then
  run_group 'frontend dependency install' npm --prefix web ci
fi
run_group 'frontend tests' npm --prefix web test
run_group 'frontend production build' npm --prefix web run build

compose_available=false
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  if docker compose version >/dev/null 2>&1 || \
    (command -v docker-compose >/dev/null 2>&1 && docker-compose version >/dev/null 2>&1); then
    compose_available=true
  fi
fi
if [ "$compose_available" = true ]; then
  run_group 'Compose current migration provision restart and business mock smoke' \
    env DEPLOY_QA_BUSINESS=true scripts/deployment-qa.sh
else
  skip_group 'Compose current migration provision restart and business mock smoke' \
    'Docker daemon and docker compose/docker-compose were not both available; no Compose result is claimed.'
fi

printf '\n===== current MVP verification summary =====\n'
printf '%-62s %-7s %8s\n' 'group' 'status' 'seconds'
printf '%-62s %-7s %8s\n' '--------------------------------------------------------------' '-------' '--------'
for i in "${!result_names[@]}"; do
  printf '%-62s %-7s %8s\n' "${result_names[$i]}" "${result_statuses[$i]}" "${result_seconds[$i]}"
done
printf 'database: %s\n' "$TEST_DATABASE_URL"
printf 'logs: %s\n' "$log_dir"

if [ "$remove_logs" = true ] && [ "$failed" -eq 0 ]; then
  rm -rf "$log_dir"
  printf 'temporary logs removed after successful run\n'
fi

if [ "$failed" -ne 0 ]; then
  printf 'current MVP verification failed; failed group logs are retained at %s\n' "$log_dir" >&2
  exit 1
fi
if [ "$skipped" -ne 0 ]; then
  printf 'current MVP verification completed with %s explicit skip(s)\n' "$skipped"
fi
