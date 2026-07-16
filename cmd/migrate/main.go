package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/baowk/dilu-go-kit/migratex"
)

func main() {
	cmd, opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		usage()
		os.Exit(2)
	}
	switch cmd {
	case "create":
		up, down, err := migratex.Create(opts.dir, opts.name)
		must(err)
		fmt.Println(up)
		fmt.Println(down)
	case "up":
		must(migratex.Up(opts.dsn, opts.dir))
	case "down":
		must(migratex.Down(opts.dsn, opts.dir, opts.steps))
	case "version":
		v, dirty, err := migratex.Version(opts.dsn, opts.dir)
		must(err)
		fmt.Printf("version=%d dirty=%v\n", v, dirty)
	case "force":
		must(migratex.Force(opts.dsn, opts.dir, opts.version))
	}
}

type cliOptions struct {
	dsn     string
	dir     string
	steps   int
	version int
	name    string
}

func parseArgs(args []string) (string, cliOptions, error) {
	commandIndex := -1
	command := ""
	valueFlags := map[string]bool{"-dsn": true, "-dir": true, "-steps": true, "-version": true, "-name": true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if valueFlags[arg] {
			i++
			continue
		}
		switch arg {
		case "create", "up", "down", "version", "force":
			if command != "" {
				return "", cliOptions{}, fmt.Errorf("multiple commands: %s and %s", command, arg)
			}
			command, commandIndex = arg, i
		}
	}
	if commandIndex < 0 {
		return "", cliOptions{}, fmt.Errorf("missing command")
	}
	flagArgs := append([]string{}, args[:commandIndex]...)
	flagArgs = append(flagArgs, args[commandIndex+1:]...)

	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	opts := cliOptions{}
	fs.StringVar(&opts.dsn, "dsn", os.Getenv("DATABASE_DSN"), "PostgreSQL DSN")
	fs.StringVar(&opts.dir, "dir", "migrations", "migration directory")
	fs.IntVar(&opts.steps, "steps", 1, "migration steps for down")
	fs.IntVar(&opts.version, "version", 0, "version for force")
	fs.StringVar(&opts.name, "name", "", "migration name for create")
	if err := fs.Parse(flagArgs); err != nil {
		return "", cliOptions{}, err
	}
	if fs.NArg() != 0 {
		return "", cliOptions{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	return command, opts, nil
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  migrate -dir services/order-service/migrations create -name init
  migrate -dsn "$DATABASE_DSN" -dir services/order-service/migrations up
  migrate -dsn "$DATABASE_DSN" -dir services/order-service/migrations down -steps 1
  migrate -dsn "$DATABASE_DSN" -dir services/order-service/migrations version
  migrate -dsn "$DATABASE_DSN" -dir services/order-service/migrations force -version 20260709120000
`)
}
