package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/viviwu/tradeforge/internal/config"
	"github.com/viviwu/tradeforge/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate up|down")
		os.Exit(2)
	}
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, cfg.DBDSN)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer st.Close()
	switch os.Args[1] {
	case "up":
		err = store.Migrate(ctx, st.Pool())
	case "down":
		err = store.MigrateDown(ctx, st.Pool())
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("migration ok")
}
