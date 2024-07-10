package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/urfave/cli/v2"
)

const (
	openAIAPIURL = "https://api.openai.com/v1/chat/completions"
)

func main() {
	app := &cli.App{
		Name:  "autogcm",
		Usage: "Automatically generate git commit messages using GPT-4",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "stream",
				Aliases: []string{"s"},
				Usage:   "Stream the output",
			},
		},
		Action: func(c *cli.Context) error {
			stream := c.Bool("stream")
			message, err := generateCommitMessage(stream)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %s\n", err)
				return cli.Exit("", 1)
			}
			if !stream {
				fmt.Print(message)
			}
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		os.Exit(1)
	}
}

func generateCommitMessage(stream bool) (string, error) {
	repo, err := git.PlainOpen(".")
	if err != nil {
		return "", fmt.Errorf("failed to open repository: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed to get worktree: %w", err)
	}

	status, err := worktree.Status()
	if err != nil {
		return "", fmt.Errorf("failed to get status: %w", err)
	}

	stagedFiles := getStagedFiles(status)

	if len(stagedFiles) == 0 {
		return "", fmt.Errorf("no staged files found")
	}

	prompt := fmt.Sprintf("Given the following staged files, generate a concise and informative git commit message:\n%s", strings.Join(stagedFiles, "\n"))

	return callGPT4API(prompt, stream)
}

func getStagedFiles(status git.Status) []string {
	var stagedFiles []string
	for file, fileStatus := range status {
		if fileStatus.Staging == git.Added || fileStatus.Staging == git.Modified {
			stagedFiles = append(stagedFiles, file)
		}
	}
	return stagedFiles
}

func callGPT4API(prompt string, stream bool) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY environment variable is not set")
	}

	fmt.Println(prompt)

	requestBody, err := json.Marshal(map[string]interface{}{
		"model": "gpt-4",
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are a helpful assistant that generates concise and informative git commit messages.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"max_tokens": 100,
		"stream":     stream,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", openAIAPIURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if stream {
		return streamResponse(resp.Body)
	}

	return parseNonStreamResponse(resp.Body)
}

func streamResponse(body io.Reader) (string, error) {
	scanner := bufio.NewScanner(body)
	var fullMessage strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var response struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(data), &response); err != nil {
				return "", err
			}

			if len(response.Choices) > 0 && response.Choices[0].Delta.Content != "" {
				content := response.Choices[0].Delta.Content
				fmt.Print(content)
				fullMessage.WriteString(content)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return fullMessage.String(), nil
}

func parseNonStreamResponse(body io.Reader) (string, error) {
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return "", err
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no response from GPT-4")
	}

	return strings.TrimSpace(response.Choices[0].Message.Content), nil
}
