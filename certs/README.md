# certs

Drop root CA certificates here (`*.crt`, PEM) that the build container has to trust.

This is only needed when something re-signs HTTPS on the way out — a debugging proxy such as Fiddler, or a corporate TLS-inspection appliance. Windows trusts that CA, a fresh Linux container does not, so `go mod tidy` fails with `x509: certificate signed by unknown authority`.

Run `.\export-proxy-ca.ps1` from the repo root to pull the CA out of the Windows store automatically.

These certificates are trusted **only inside the build image**, and only for fetching Go modules. They never reach the shipped `MovieFinder.exe`, which uses the Windows certificate store at runtime.
