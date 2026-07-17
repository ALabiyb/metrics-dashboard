# Contributing

Thanks for your interest.

## Reporting bugs

Open an issue with the bug template. Include the version/image, deployment mode (in-cluster/standalone/simulator), and Kubernetes + Prometheus versions if relevant.

## Sending a PR

1. Fork + branch
2. Small, focused commits (`git log` shows the tone)
3. `gofmt -s -w .` before pushing
4. `go build ./... && go vet ./...` all green
5. Open PR against `main` with what changed and why. Screenshots for UI changes.

## Areas where help is welcome

- Test coverage
- Support for more Prometheus flavours (Mimir, VictoriaMetrics)
- Object-storage panel (S3/MinIO metrics)
- Mobile-friendly layout

## License

By contributing, you agree that your contributions are licensed under MIT.
