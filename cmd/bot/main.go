package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

type botConfig struct {
	URL     string
	Players int
	MatchID string
}

func main() {
	url := flag.String("url", "ws://localhost:8080/ws", "WebSocket server URL")
	players := flag.Int("players", 1, "number of bots to connect (1..4)")
	duration := flag.Duration("duration", 60*time.Second, "how long to run")
	matchID := flag.String("match", "", "join an existing match instead of creating one")
	flag.Parse()

	if *players < 1 || *players > 4 {
		fmt.Fprintln(os.Stderr, "bot: -players must be between 1 and 4")
		os.Exit(2)
	}
	if *duration <= 0 {
		fmt.Fprintln(os.Stderr, "bot: -duration must be positive")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	log.Printf("starting %d bot(s) against %s", *players, *url)
	err := run(ctx, botConfig{
		URL:     *url,
		Players: *players,
		MatchID: *matchID,
	})
	if err != nil {
		log.Printf("bot: %v", err)
		os.Exit(1)
	}
	log.Printf("bot run complete")
}

func run(ctx context.Context, config botConfig) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	created := make(chan struct{})
	var lobbyID string
	if config.MatchID != "" {
		lobbyID = config.MatchID
		close(created)
	}

	joined := make(chan struct{}, config.Players)
	errCh := make(chan error, config.Players)
	report := func(err error) {
		if err == nil {
			return
		}
		select {
		case errCh <- err:
		default:
		}
		cancel()
	}

	var wait sync.WaitGroup
	for index := 0; index < config.Players; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()

			b := newBot(index+1, config.URL, fmt.Sprintf("bot-%d", index+1))
			if err := b.connect(ctx); err != nil {
				report(fmt.Errorf("bot %d connect: %w", index+1, err))
				return
			}
			defer b.close()

			if index == 0 && config.MatchID == "" {
				id, err := b.createMatch(ctx)
				if err != nil {
					report(fmt.Errorf("bot %d: %w", index+1, err))
					return
				}
				lobbyID = id
				close(created)
			} else {
				select {
				case <-created:
				case <-ctx.Done():
					return
				}
				if err := b.joinMatch(ctx, lobbyID); err != nil {
					report(fmt.Errorf("bot %d: %w", index+1, err))
					return
				}
			}

			joined <- struct{}{}
			if index == 0 && config.MatchID == "" {
				for n := 1; n < config.Players; n++ {
					select {
					case <-joined:
					case <-ctx.Done():
						return
					}
				}
				if err := b.startMatch(ctx); err != nil {
					report(fmt.Errorf("bot %d: %w", index+1, err))
					return
				}
			}

			if err := b.play(ctx); err != nil {
				report(fmt.Errorf("bot %d play: %w", index+1, err))
			}
		}(index)
	}

	wait.Wait()
	close(errCh)

	var first error
	for err := range errCh {
		if first == nil {
			first = err
		}
	}
	return first
}
