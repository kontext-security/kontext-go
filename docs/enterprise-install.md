# Enterprise Install

Default Go module install:

```sh
go get github.com/kontext-security/kontext-go@v0.1.2
```

Private enterprise install:

```sh
go env -w GOPRIVATE=github.enterprise.example.com/kontext/*
go env -w GONOSUMDB=github.enterprise.example.com/kontext/*
```

Supported distribution paths:

- GitHub module path with semver tags.
- GitHub Enterprise, GitLab, Bitbucket, JFrog Artifactory, Google Artifact Registry, or customer-managed private Go proxy.
- ECR for containers and OCI artifacts only.

Do not position AWS CodeArtifact as the primary native Go module host. Use generic packages there only as a fallback archive path.
