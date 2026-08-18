package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	gioapp "gioui.org/app"

	"firstlight/internal/app"
	"firstlight/internal/ilo"
	"firstlight/internal/keyboardmap"
	"firstlight/internal/login"
)

func main() {
	var cfg app.Config
	var lang string
	flag.StringVar(&cfg.Addr, "addr", "", "iLO address, optionally address:https_port")
	flag.StringVar(&cfg.User, "name", "", "iLO user name")
	flag.StringVar(&cfg.Password, "password", "", "iLO password")
	flag.StringVar(&cfg.ISOPath, "iso", "", "local ISO file to mount as virtual CD/DVD media")
	flag.StringVar(&lang, "lang", "en", "accepted for HPLOCONS CLI compatibility")
	flag.BoolVar(&cfg.Share, "share", false, "request shared console when iLO reports the console is busy")
	flag.BoolVar(&cfg.Seize, "seize", false, "seize console when iLO reports the console is busy")
	flag.BoolVar(&cfg.VerifyCert, "verify-cert", false, "verify the iLO HTTPS certificate")
	flag.BoolVar(&cfg.Debug, "debug", false, "write verbose protocol/input status to Firstlight-debug.log")
	flag.StringVar(&cfg.LogPath, "log", "", "verbose log file path; enables logging even without -debug")
	flag.Parse()
	executablePath, executableErr := os.Executable()
	if executableErr != nil {
		executablePath = os.Args[0]
	}
	keyboardMaps := keyboardmap.LoadForExecutable(executablePath)
	cfg.KeyboardMaps = keyboardMaps.Registry
	cfg.KeyboardMapDir = keyboardMaps.Directory
	cfg.KeyboardMapWarnings = keyboardMaps.Warnings
	if executableErr != nil {
		cfg.KeyboardMapWarnings = append(cfg.KeyboardMapWarnings, fmt.Sprintf("Executable path could not be resolved exactly: %v", executableErr))
	}

	_ = lang
	if cfg.Share && cfg.Seize {
		log.Fatal("-share and -seize are mutually exclusive")
	}

	// Gio requires the OS main thread; the application logic runs beside it
	// and terminates the process when the last window is done.
	go func() {
		os.Exit(run(cfg))
	}()
	gioapp.Main()
}

func run(cfg app.Config) int {
	if cfg.Addr == "" || cfg.User == "" || cfg.Password == "" {
		base := cfg
		sessions := map[string]*app.SessionWindow{}
		err := login.RunLauncher(context.Background(), login.Fields{
			Addr: cfg.Addr,
			User: cfg.User,
		}, func(fields login.Fields) error {
			key := fields.IdentityKey()
			if session := sessions[key]; session != nil {
				session.Focus()
				return nil
			}
			sessionCfg := base
			sessionCfg.Addr = fields.Addr
			sessionCfg.User = fields.User
			sessionCfg.Password = fields.Password
			session, err := app.OpenSession(context.Background(), sessionCfg, func() {
				delete(sessions, key)
			})
			if err != nil {
				return err
			}
			sessions[key] = session
			session.Focus()
			return nil
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for _, session := range sessions {
			session.Close()
		}
		return 0
	}

	if cfg.Addr != "" {
		host, _, err := ilo.ParseAddress(cfg.Addr)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if strings.TrimSpace(host) == "" {
			fmt.Fprintln(os.Stderr, "-addr must contain a host")
			return 1
		}
	}

	if err := app.Run(context.Background(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
