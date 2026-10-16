// Command fastci mirrors a Workspace into a GitHub repo that builds and
// publishes the Workspace's image.
package main

import (
	"os"

	"github.com/Baiyuani/FastCI/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
