package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/baowk/dilu-go-kit/migratex"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_DSN"), "PostgreSQL DSN, defaults to DATABASE_DSN")
	dir := flag.String("dir", "migrations", "migration directory")
	steps := flag.Int("steps", 1, "migration steps for down")
	version := flag.Int("version", 0, "version for force")
	name := flag.String("name", "", "migration name for create")
	flag.Parse()

	if flag.NArg() < 1 {
		usage()
		os.Exit(2)
	}

	cmd := flag.Arg(0)
	switch cmd {
	case "create":
		up, down, err := migratex.Create(*dir, *name)
		must(err)
		fmt.Println(up)
		fmt.Println(down)
	case "up":
		must(migratex.Up(*dsn, *dir))
	case "down":
		must(migratex.Down(*dsn, *dir, *steps))
	case "version":
		v, dirty, err := migratex.Version(*dsn, *dir)
		must(err)
		fmt.Printf("version=%d dirty=%v\n", v, dirty)
	case "force":
		must(migratex.Force(*dsn, *dir, *version))
	default:
		usage()
		os.Exit(2)
	}
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
