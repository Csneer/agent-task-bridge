package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	client "agent-task-bridge/clients/go"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: bridge-client call METHOD --out PRIVATE_FILE | retry UUID --out PRIVATE_FILE")
	}
	flags := flag.NewFlagSet("bridge-client", flag.ContinueOnError)
	output := flags.String("out", "", "new private result file")
	if e := flags.Parse(os.Args[3:]); e != nil {
		return e
	}
	if *output == "" {
		return fmt.Errorf("--out required; results can contain lease secrets")
	}
	c, e := client.New(os.Getenv("BRIDGE_URL"), os.Getenv("BRIDGE_TOKEN"), os.Getenv("BRIDGE_AGENT"), os.Getenv("BRIDGE_JOURNAL"))
	if e != nil {
		return e
	}
	var result client.Outcome
	switch os.Args[1] {
	case "call":
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 262145))
		if err != nil {
			return err
		}
		if len(raw) > 262144 {
			return fmt.Errorf("params exceed limit")
		}
		value, err := client.Parse(raw)
		if err != nil {
			return err
		}
		params, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("params must be object")
		}
		result, e = c.Call(context.Background(), os.Args[2], params)
	case "retry":
		result, e = c.Retry(context.Background(), os.Args[2])
	default:
		return fmt.Errorf("unknown operation")
	}
	if result.Key != "" {
		fmt.Fprintln(os.Stderr, "idempotency_key="+result.Key)
	}
	if len(result.Response) > 0 {
		if err := client.PrivateWrite(*output, result.Response); err != nil {
			return fmt.Errorf("cannot save private result: %w", err)
		}
	}
	return e
}
