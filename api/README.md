# API contracts

Proto files under this directory are the source of truth for service
contracts. Keep APIs versioned (`<service>/v1`) and generate transport code
instead of hand-writing gRPC registration or HTTP gateway bindings.

From the repository root:

```bash
make buf-install       # optional; installs a pinned Buf binary in ./bin
make proto-lint
make proto-generate
```

Generated files are written to `gen/` (ignored by Git). CI should run
`make proto-lint proto-generate` and fail when generated output differs from
the checked-in generated artifacts in a service repository.
