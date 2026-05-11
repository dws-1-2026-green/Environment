package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

var errExit = errors.New("exit")

func main() {
	eventReceiverURL := flag.String("url", "http://event-receiver.localhost", "Event Receiver base URL")
	source := flag.String("source", "manual-sender", "Event source name")
	subsAPI := flag.String("subs-api", "http://subscriptions.localhost", "Subscriptions API URL")
	port := flag.String("port", "8888", "Local port to listen on for incoming webhooks")
	flag.Parse()

	sender := NewSender(*eventReceiverURL, *source)
	server := NewWebhookServer(*subsAPI, *port)

	if err := server.Start(":" + *port); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start webhook server: %v\n", err)
		os.Exit(1)
	}

	printBanner(*eventReceiverURL, *source, *subsAPI, *port, server.instanceID)

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		err := handleCommand(line, sender, server)
		if errors.Is(err, errExit) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			server.Stop(ctx)
			cancel()
			fmt.Println("Goodbye!")
			return
		}
		if err != nil {
			fmt.Printf("Error: %v\n", err)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		os.Exit(1)
	}
}

func handleCommand(input string, sender *Sender, server *WebhookServer) error {
	parts := strings.SplitN(input, " ", 4)
	switch parts[0] {
	case "exit", "quit":
		return errExit

	case "send":
		if len(parts) < 4 {
			fmt.Println("Usage: send <count> <event_type> <json_payload>")
			fmt.Println(`Example: send 1 order.created {"order_id":"123","amount":100}`)
			return nil
		}
		count, err := strconv.Atoi(parts[1])
		if err != nil || count <= 0 {
			fmt.Printf("count must be a positive integer, got: %s\n", parts[1])
			return nil
		}
		data, err := parsePayload(parts[3])
		if err != nil {
			return err
		}
		return sender.SendEvent(count, parts[2], data)

	case "subscribe":
		if len(parts) < 3 {
			fmt.Println("Usage: subscribe <event_type> <source> [webhook_path]")
			return nil
		}
		webhookPath := ""
		if len(parts) > 3 {
			webhookPath = strings.TrimSpace(parts[3])
		}
		return server.Subscribe(context.Background(), parts[1], parts[2], webhookPath)

	case "list":
		return server.ListSubscriptions(context.Background())

	case "help":
		printHelp()
		return nil

	default:
		fmt.Printf("Unknown command: %q. Type 'help' for available commands.\n", parts[0])
		return nil
	}
}

func printBanner(url, source, subsAPI, port, instanceID string) {
	fmt.Println("════════════════════════════════════════════════")
	fmt.Println("           Webhook Testing Tool")
	fmt.Println("════════════════════════════════════════════════")
	fmt.Printf("Event Receiver:    %s  (source: %s)\n", url, source)
	fmt.Printf("Subscriptions API: %s\n", subsAPI)
	fmt.Printf("Webhook listener:  http://localhost:%s  (instance: %s)\n", port, instanceID)
	fmt.Println()
	printHelp()
}

func printHelp() {
	fmt.Println("Commands:")
	fmt.Println("  send <count> <event_type> <json_payload>   — send events to Event Receiver")
	fmt.Println("  subscribe <event_type> <source> [path]     — subscribe to webhook deliveries")
	fmt.Println("  list                                        — list active subscriptions")
	fmt.Println("  help                                        — show this message")
	fmt.Println("  exit")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println(`  send 5 order.created {"order_id":"123","amount":100}`)
	fmt.Println("  subscribe order.created test-system")
	fmt.Println()
}
