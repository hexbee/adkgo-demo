package webapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gorilla/mux"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

const (
	sessionTitleStateKey       = "title"
	sessionTitleSourceStateKey = "title_source"
	sessionTitleSourceModel    = "model"
	sessionTitleSourceFallback = "fallback"
	maxSessionTitleRunes       = 32
)

type titleGenerator interface {
	Generate(context.Context, string, string) (string, error)
}

type llmTitleGenerator struct {
	llm model.LLM
}

func newLLMTitleGenerator(llm model.LLM) titleGenerator {
	if llm == nil {
		return nil
	}
	return &llmTitleGenerator{llm: llm}
}

func (g *llmTitleGenerator) Generate(ctx context.Context, question, answer string) (string, error) {
	temperature := float32(0.2)
	request := &model.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText(fmt.Sprintf(
			"<question>\n%s\n</question>\n<answer>\n%s\n</answer>",
			truncateRunes(question, 2000), truncateRunes(answer, 4000),
		), genai.RoleUser)},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(
				"Create a concise title for this chat from its first question and answer. "+
					"Use the same language as the question, capture the concrete task, and avoid generic wording. "+
					"Use at most 18 Chinese characters or 8 words, never exceed 32 Unicode characters, and return only the title without quotes or punctuation.",
				genai.RoleUser,
			),
			Temperature:     &temperature,
			MaxOutputTokens: 96,
		},
	}

	var output strings.Builder
	for response, err := range g.llm.GenerateContent(ctx, request, false) {
		if err != nil {
			return "", err
		}
		if response == nil || response.Content == nil {
			continue
		}
		for _, part := range response.Content.Parts {
			if part != nil && part.Text != "" && !part.Thought {
				output.WriteString(part.Text)
			}
		}
	}

	title := normalizeSessionTitle(output.String())
	if title == "" {
		return "", errors.New("title model returned no usable text")
	}
	return title, nil
}

func sessionTitleHandler(service session.Service, generator titleGenerator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		result, err := service.Get(r.Context(), &session.GetRequest{
			AppName: vars["app_name"], UserID: vars["user_id"], SessionID: vars["session_id"],
		})
		if err != nil {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}

		existingTitle := titleFromState(result.Session)
		existingSource := titleSourceFromState(result.Session)
		question, answer := firstSessionExchange(result.Session)

		if existingTitle != "" && existingSource != "" && existingSource != sessionTitleSourceFallback {
			writeTitleResponse(w, existingTitle, existingSource)
			return
		}
		if existingTitle != "" && existingSource == "" && (question == "" || existingTitle != fallbackSessionTitle(question)) {
			if err := persistSessionTitle(r.Context(), service, result.Session, existingTitle, sessionTitleSourceModel); err != nil {
				http.Error(w, "could not migrate session title", http.StatusInternalServerError)
				return
			}
			writeTitleResponse(w, existingTitle, sessionTitleSourceModel)
			return
		}
		if question == "" || answer == "" {
			if existingTitle != "" {
				writeTitleResponse(w, existingTitle, sessionTitleSourceFallback)
				return
			}
			http.Error(w, "the first exchange is not complete", http.StatusConflict)
			return
		}

		title := fallbackSessionTitle(question)
		source := sessionTitleSourceFallback
		if generator != nil {
			generated, generateErr := generator.Generate(r.Context(), question, answer)
			if generateErr != nil {
				log.Printf("session title generation failed; using first-question fallback: %v", generateErr)
			} else if generated != "" {
				title = generated
				source = sessionTitleSourceModel
			}
		}

		if title == existingTitle && source == existingSource {
			writeTitleResponse(w, title, source)
			return
		}
		if err := persistSessionTitle(r.Context(), service, result.Session, title, source); err != nil {
			http.Error(w, "could not save session title", http.StatusInternalServerError)
			return
		}
		writeTitleResponse(w, title, source)
	}
}

func persistSessionTitle(ctx context.Context, service session.Service, sess session.Session, title, source string) error {
	timestamp := time.Now()
	if !timestamp.After(sess.LastUpdateTime()) {
		timestamp = sess.LastUpdateTime().Add(time.Microsecond)
	}
	event := &session.Event{
		ID:        newTitleEventID(),
		Author:    "session_title",
		Timestamp: timestamp,
		Actions: session.EventActions{
			StateDelta: map[string]any{
				sessionTitleStateKey:       title,
				sessionTitleSourceStateKey: source,
			},
		},
	}
	return service.AppendEvent(ctx, sess, event)
}

func writeTitleResponse(w http.ResponseWriter, title, source string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"title": title, "source": source})
}

func titleFromState(sess session.Session) string {
	value, err := sess.State().Get(sessionTitleStateKey)
	if err != nil {
		return ""
	}
	title, ok := value.(string)
	if !ok {
		return ""
	}
	return normalizeSessionTitle(title)
}

func titleSourceFromState(sess session.Session) string {
	value, err := sess.State().Get(sessionTitleSourceStateKey)
	if err != nil {
		return ""
	}
	source, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(source)
}

func firstSessionExchange(sess session.Session) (string, string) {
	var question string
	var answers []string
	for event := range sess.Events().All() {
		text := visibleEventText(event)
		if question == "" {
			if event.Author == "user" && text != "" {
				question = text
			}
			continue
		}
		if event.Author == "user" && text != "" {
			break
		}
		if event.Author != "user" && text != "" {
			answers = append(answers, text)
		}
	}
	return question, strings.Join(answers, "\n")
}

func visibleEventText(event *session.Event) string {
	if event == nil || event.Content == nil {
		return ""
	}
	var text strings.Builder
	for _, part := range event.Content.Parts {
		if part != nil && part.Text != "" && !part.Thought {
			text.WriteString(part.Text)
		}
	}
	return strings.TrimSpace(text.String())
}

func fallbackSessionTitle(question string) string {
	return normalizeSessionTitle(question)
}

func normalizeSessionTitle(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "`", ""))
	if line, _, ok := strings.Cut(value, "\n"); ok {
		value = line
	}
	value = strings.Join(strings.Fields(value), " ")
	for _, prefix := range []string{"Session title:", "Session Title:", "Title:", "title:", "标题：", "标题:"} {
		value = strings.TrimSpace(strings.TrimPrefix(value, prefix))
	}
	value = strings.Trim(value, " \t\r\n\"'“”‘’「」『』#*_.,，。!！?？:：;；-|—")
	return truncateRunes(value, maxSessionTitleRunes)
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || value == "" || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit]))
}

func newTitleEventID() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err == nil {
		return "title-" + hex.EncodeToString(id[:])
	}
	return fmt.Sprintf("title-%d", time.Now().UnixNano())
}
