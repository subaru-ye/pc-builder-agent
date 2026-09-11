// searchquota lets the offline collector share the product's atomic search budget.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/subaru-ye/pc-builder-agent/internal/dotenv"
	"github.com/subaru-ye/pc-builder-agent/internal/store"
	"os"
	"time"
)

func main() {
	usage := flag.Int("usage", 0, "provider month usage")
	budget := flag.Int("budget", 240, "shared month budget")
	flag.Parse()
	dotenv.Load(".env")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, e := store.New(ctx, os.Getenv("PG_DSN"))
	if e == nil {
		defer st.Close()
		e = st.ReserveSearch(ctx, *usage, *budget)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, "search quota unavailable or exhausted")
		os.Exit(1)
	}
}
