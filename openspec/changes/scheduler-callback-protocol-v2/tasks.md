## 1. Protocol and configuration

- [ ] 1.1 Add RED tests that freeze legacy callback body/header behavior and
  reject partial v2 auth configuration.
- [ ] 1.2 Add opt-in v2 callback auth configuration and validation without
  logging or exposing the HMAC secret.
- [ ] 1.3 Implement canonical v2 envelope, delivery metadata, exact-body
  digest, reserved headers, and HMAC signer.

## 2. Artifact binding

- [ ] 2.1 Add RED tests for canonical sequence-bundle digest, absent output,
  symlink/path traversal, invalid JSON, and non-success terminal callbacks.
- [ ] 2.2 Implement server-rooted controlled `sequence_bundle/v1` collector
  and signed bounded error outcome.

## 3. Regression and documentation

- [ ] 3.1 Add verification tests for signature tampering, header conflict,
  distinct delivery ids, timestamp shape, and legacy callback regression.
- [ ] 3.2 Document v2 configuration and wire contract without adding secrets
  to example configs.
- [ ] 3.3 Run focused and broader scheduler tests in the approved remote test
  environment; freeze a clean candidate and audit the protocol.

## 4. Cross-repository enablement gate

- [ ] 4.1 Update Runtime V2 parser/composition only after the scheduler
  candidate contract is frozen; prove header/body parity and protected-only
  routing.
- [ ] 4.2 Require clean deployment identity, service/output-root ownership,
  namespace isolation, and no-preemption evidence before any configuration
  change or one-job E2E.
