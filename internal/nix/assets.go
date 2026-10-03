// Package container embeds the files needed to build Pact's agent image.
package container

import "embed"

// BuildContext is the Dockerfile and its COPY inputs.
//
//go:embed Dockerfile flake.nix flake.lock nix.conf entrypoint.sh
var BuildContext embed.FS
