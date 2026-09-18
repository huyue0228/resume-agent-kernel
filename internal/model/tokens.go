package model

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

//go:embed bpe_data/cl100k_base.tiktoken
var vocabulary []byte

//go:embed bpe_data/o200k_base.tiktoken
var modernVocabulary []byte

// Tokenizer data is embedded: counting must never access the network or host cache.
type embeddedLoader struct{}

func (embeddedLoader) LoadTiktokenBpe(name string) (map[string]int, error) {
	data := vocabulary
	if strings.HasSuffix(name, "/o200k_base.tiktoken") {
		data = modernVocabulary
	} else if !strings.HasSuffix(name, "/cl100k_base.tiktoken") {
		return nil, fmt.Errorf("unsupported embedded encoding")
	}
	ranks := make(map[string]int)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid embedded vocabulary")
		}
		token, err := base64.StdEncoding.DecodeString(parts[0])
		if err != nil {
			return nil, err
		}
		rank, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, err
		}
		ranks[string(token)] = rank
	}
	return ranks, scanner.Err()
}

var encoderOnce sync.Once
var encoderMu sync.Mutex
var encoders = map[string]*tiktoken.Tiktoken{}

// TokenCounter uses a known, offline baseline and calibrates it per conversation
// from provider usage. cl100k is an estimate, not a claim about the provider's tokenizer.
type TokenCounter struct {
	ratio    float64
	observed bool
	encoder  *tiktoken.Tiktoken
	encoding string
}

func NewTokenCounter() (*TokenCounter, error) {
	return NewTokenCounterForModel("")
}
func NewTokenCounterForModel(name string) (*TokenCounter, error) {
	encoding := "cl100k_base"
	name = strings.ToLower(name)
	for _, prefix := range []string{"gpt-4o", "gpt-4.1", "gpt-5", "o1", "o3", "o4"} {
		if strings.HasPrefix(name, prefix) {
			encoding = "o200k_base"
			break
		}
	}
	encoderOnce.Do(func() { tiktoken.SetBpeLoader(embeddedLoader{}) })
	encoderMu.Lock()
	defer encoderMu.Unlock()
	enc, ok := encoders[encoding]
	if !ok {
		var err error
		enc, err = tiktoken.GetEncoding(encoding)
		if err != nil {
			return nil, err
		}
		encoders[encoding] = enc
	}
	return &TokenCounter{ratio: 1, encoder: enc, encoding: encoding}, nil
}
func (c *TokenCounter) Name() string { return c.encoding + "_calibrated" }

func (c *TokenCounter) Text(text string) int { return len(c.encoder.EncodeOrdinary(text)) }
func (c *TokenCounter) Base(messages []Message) int {
	total := 8
	for _, message := range messages {
		total += c.Text(message.Content) + 16
	}
	return total
}
func (c *TokenCounter) Estimate(messages []Message) int {
	return int(math.Ceil(float64(c.Base(messages)) * c.ratio * 1.10))
}
func (c *TokenCounter) Observe(messages []Message, input int) {
	if input <= 0 {
		return
	}
	// A provider's first observed usage replaces the generic estimate. Retain a
	// small margin and decay slowly so a changing message mix cannot erase a spike.
	ratio := math.Max(.25, math.Min(4, float64(input)/float64(c.Base(messages))))
	if !c.observed {
		c.ratio = ratio
		c.observed = true
	} else {
		c.ratio = math.Max(ratio, c.ratio*.9)
	}
}
