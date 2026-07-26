package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "dbprompter",
	Short: "DB Prompter — explore any database and query it with AI",
	Long: `DB Prompter is a single-binary tool that connects to PostgreSQL, MySQL,
SQL Server, or SQLite databases, indexes their metadata (tables, columns,
foreign keys), lets you tag tables with plain-language descriptions, and
generates SQL queries with AI.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
