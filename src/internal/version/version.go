package version

// Version is set at build time via ldflags:
//
//	go build -ldflags "-X github.com/shchepetkov/sherlockops/internal/version.Version=v1.0.0"
var Version = "dev"
