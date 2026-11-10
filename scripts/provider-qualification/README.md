# Provider qualification

This command performs one bounded OpenAI-compatible Chat qualification request against a real provider. It uses the existing `internal/provider` client, so endpoint validation, no-redirect behavior, timeout phases, response validation, final usage handling, and failure classification stay identical to the gateway path.

The command requires an HTTPS base URL, an API key, and a model ID. Credentials are read from environment variables and never appear in the JSON result.

```sh
PROVIDER_QUALIFICATION_BASE_URL='https://gateway.example/v1' \
PROVIDER_QUALIFICATION_API_KEY="$PROVIDER_API_KEY" \
PROVIDER_QUALIFICATION_MODEL='gpt-4o-mini' \
go run ./scripts/provider-qualification
```

The default request asks the model to return `OK` with an output bound of eight tokens. The command succeeds only when the provider returns a valid terminal response and confirmed final usage. The result includes the protocol outcome, finish reason, usage, and a SHA-256 digest of the sanitized response. It does not print response text, upstream bodies, request IDs, URLs containing credentials, or raw errors.

This is a supplier qualification probe. It does not create a platform model, route, price, tenant, wallet, reservation, attempt, or settlement. Passing it does not certify a supplier for every protocol, model, pricing dimension, or production traffic pattern.
