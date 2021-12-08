package cmd

import (
	"fmt"

	"github.com/flanksource/commons/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var Root = &cobra.Command{
	Use: "confighub",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		count, _ := cmd.Flags().GetCount("loglevel")
		// logger.StandardLogger().(logsrusapi.Logger).Out = os.Stderr
		logger.StandardLogger().SetLogLevel(count)
		logger.UseZap(cmd.Flags())

	},
}

var dev bool
var httpPort, metricsPort, devGuiPort int
var configFiles []string

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func ServerFlags(flags *pflag.FlagSet) {
	flags.IntVar(&httpPort, "httpPort", 8080, "Port to expose a health dashboard ")
	flags.IntVar(&devGuiPort, "devGuiPort", 3004, "Port used by a local npm server in development mode")
	flags.IntVar(&metricsPort, "metricsPort", 8081, "Port to expose a health dashboard ")
	flags.BoolVar(&dev, "dev", false, "Run in development mode")
}

func init() {
	logger.BindFlags(Root.PersistentFlags())

	if len(commit) > 8 {
		version = fmt.Sprintf("%v, commit %v, built at %v", version, commit[0:8], date)
	}
	Root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version of confighub",
		Args:  cobra.MinimumNArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version)
		},
	})

	Root.AddCommand(Run, Analyze)
}
