package cmd

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/adharshmk96/dbprompter/internal/ai"
	"github.com/adharshmk96/dbprompter/internal/indexer"
	"github.com/adharshmk96/dbprompter/internal/store"
	"github.com/adharshmk96/dbprompter/internal/web"
	"github.com/spf13/cobra"
)

var (
	servePort int
	dataPath  string
	noOpen    bool
	portTries = 100
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the DB Prompter web dashboard",
	Args:  cobra.NoArgs,
	RunE:  runServe,
}

func runServe(cmd *cobra.Command, args []string) error {
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

	ln, err := listenFromPort(servePort)
	if err != nil {
		return err
	}
	defer ln.Close()

	url := fmt.Sprintf("http://%s", ln.Addr().String())
	log.Printf("DB Prompter listening on %s (data: %s)", url, dataPath)
	if !noOpen {
		openBrowser(url)
	}
	return srv.Serve(ln)
}

// listenFromPort binds 127.0.0.1, walking upward from port until a free one is
// found (or portTries ports have been tried).
func listenFromPort(port int) (net.Listener, error) {
	for p := port; p < port+portTries && p <= 65535; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			if p != port {
				log.Printf("port %d in use, using %d instead", port, p)
			}
			return ln, nil
		}
	}
	return nil, fmt.Errorf("no free port found in range %d-%d", port, port+portTries-1)
}

// openBrowser best-effort launches the platform's default browser.
func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	if err := c.Start(); err != nil {
		log.Printf("could not open browser: %v", err)
	}
}

func init() {
	registerServeFlags(rootCmd)
	registerServeFlags(serveCmd)
	rootCmd.AddCommand(serveCmd)
}

func registerServeFlags(cmd *cobra.Command) {
	cmd.Flags().IntVarP(&servePort, "port", "p", 8080, "port to listen on (next free port is used if taken)")
	cmd.Flags().StringVarP(&dataPath, "data", "d", "", "path to the app database (default ~/.dbprompter/app.db)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "do not open the browser on start")
}
