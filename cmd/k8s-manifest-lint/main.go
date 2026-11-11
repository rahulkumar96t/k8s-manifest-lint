// Command k8s-manifest-lint checks rendered Kubernetes manifests for
// reliability and security problems. See the README for the rule set.
package main

import (
	"os"

	"github.com/rahulkumar96t/k8s-manifest-lint/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
