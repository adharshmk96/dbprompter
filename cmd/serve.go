package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/adharshmk96/dbprompter/internal/ai"
	"github.com/adharshmk96/dbprompter/internal/indexer"
	"github.com/adharshmk96/dbprompter/internal/store"
	"github.com/adharshmk96/dbprompter/internal/web"
	"github.com/spf13/cobra"
)

var (
	servePort int
	dataPath  string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the DB Prompter web dashboard",
	RunE: func(cmd *cobra.Command, args []string) error {
		if dataPath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home dir: %w", err)
			}
			dir := filepath.Join(home, ".dbprompter")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create data dir: %w", err)
			}
			dataPath = filepath.Join(dir, "app.db")
		}

		st, err := store.Open(dataPath)
		if err != nil {
			return fmt.Errorf("open app store: %w", err)
		}
		defer st.Close()

		idx := indexer.New(st)
		aiSvc := ai.NewService(st)

		srv := web.New(st, idx, aiSvc)
		addr := fmt.Sprintf("127.0.0.1:%d", servePort)
		log.Printf("DB Prompter listening on http://%s (data: %s)", addr, dataPath)
		return srv.Listen(addr)
	},
}

func init() {
	serveCmd.Flags().IntVarP(&servePort, "port", "p", 8080, "port to listen on")
	serveCmd.Flags().StringVarP(&dataPath, "data", "d", "", "path to the app database (default ~/.dbprompter/app.db)")
	rootCmd.AddCommand(serveCmd)
}
