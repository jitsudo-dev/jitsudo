// Package cli implements the jitsudo CLI commands using cobra.
//
// License: Apache 2.0
package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// globalFlags are flags available on every command.
type globalFlags struct {
	serverURL  string
	token      string
	output     string
	quiet      bool
	debug      bool
	configPath string
}

var flags globalFlags

// NewRootCmd builds and returns the root cobra command.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "jitsudo",
		Short: "sudo for your cloud",
		Long: `jitsudo — Just-In-Time privileged access management for AWS, Azure, GCP, and Kubernetes.

Request temporary elevated cloud permissions through an approval workflow,
with automatic expiry and a tamper-evident audit log.

Documentation: https://jitsudo.dev/docs`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return initConfig()
		},
	}

	// Global flags
	pf := root.PersistentFlags()
	pf.StringVar(&flags.serverURL, "server", "", "Control plane URL (overrides config)")
	pf.StringVar(&flags.token, "token", "", "Bearer token (overrides stored credentials)")
	pf.StringVarP(&flags.output, "output", "o", "table", "Output format: table, json, yaml")
	pf.BoolVarP(&flags.quiet, "quiet", "q", false, "Suppress non-essential output")
	pf.BoolVar(&flags.debug, "debug", false, "Enable debug logging")
	pf.StringVar(&flags.configPath, "config", "", "Config file path (default: ~/.jitsudo/config.yaml)")

	// Subcommands
	root.AddCommand(
		newLoginCmd(),
		newRequestCmd(),
		newStatusCmd(),
		newApproveCmd(),
		newDenyCmd(),
		newExecCmd(),
		newShellCmd(),
		newRevokeCmd(),
		newAuditCmd(),
		newPolicyCmd(),
		newServerCmd(),
	)

	return root
}

// initConfig loads configuration from file and environment variables.
func initConfig() error {
	if flags.configPath != "" {
		viper.SetConfigFile(flags.configPath)
	} else {
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
		viper.AddConfigPath("$HOME/.jitsudo")
	}

	viper.SetEnvPrefix("JITSUDO")
	viper.AutomaticEnv()

	// Best-effort config load; not an error if the file doesn't exist yet.
	_ = viper.ReadInConfig()

	return nil
}
