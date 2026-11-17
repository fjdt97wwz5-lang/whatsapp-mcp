package mcptools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/style"
)

// Writing style: how the owner writes, learned from their own sent messages.

type styleIn struct{}

type correctionIn struct {
	Final string `json:"final" jsonschema:"the text the owner actually wanted (after their changes)"`
	Draft string `json:"draft,omitempty" jsonschema:"the draft you had proposed, before their changes"`
	Chat  string `json:"chat,omitempty" jsonschema:"chat or contact the message was for (optional)"`
	Note  string `json:"note,omitempty" jsonschema:"what they said about the change, in a few words (optional)"`
}

type notesIn struct {
	Text string `json:"text" jsonschema:"the note text, in the owner's words"`
	Mode string `json:"mode,omitempty" jsonschema:"append (default) adds a line; replace overwrites all notes"`
}

var errStyleOff = errors.New("the writing style is turned off (writing_style_enabled = false)")

func (d Deps) styleOn(ctx context.Context) error {
	if d.Style == nil {
		return errors.New("writing style is not available")
	}
	s, err := d.Policy.Settings(ctx)
	if err != nil {
		return err
	}
	if !s.WritingStyleEnabled {
		return errStyleOff
	}
	return nil
}

func registerStyle(s *mcp.Server, d Deps) {
	mcp.AddTool(s, &mcp.Tool{Name: "get_writing_style", Annotations: readOnly,
		Description: "Get how the owner writes on WhatsApp (length, greetings, sign-offs, emojis, tú/usted, phrases they repeat, examples, their own notes and recent corrections). " +
			"Call it before drafting any WhatsApp message in their name, and write the draft in that style."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ styleIn) (*mcp.CallToolResult, any, error) {
			if err := d.styleOn(ctx); err != nil {
				return errResult(err), nil, nil
			}
			st, err := d.Style.Get(ctx, 20)
			if err != nil {
				return errResult(err), nil, nil
			}
			return text(styleText(st)), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "save_writing_correction", Annotations: &mcp.ToolAnnotations{OpenWorldHint: boolp(false)},
		Description: "Save a correction to the owner's writing style: the draft you proposed and the text they actually wanted. " +
			"The owner asked for this to happen automatically: whenever they change a WhatsApp draft you wrote, call this without asking. Logged."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in correctionIn) (*mcp.CallToolResult, any, error) {
			if err := d.styleOn(ctx); err != nil {
				return errResult(err), nil, nil
			}
			id, err := d.Style.AddCorrection(ctx, d.actor(), in.Chat, in.Draft, in.Final, in.Note)
			if err != nil {
				return errResult(err), nil, nil
			}
			d.Policy.Audit(ctx, d.actor(), "style.correction", in.Chat, "ok", map[string]any{"id": id,
				"draft_length": len([]rune(in.Draft)), "final_length": len([]rune(in.Final))})
			return text(fmt.Sprintf("Saved correction %d. It will be used the next time you read the writing style.", id)), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "update_writing_style_notes", Annotations: admin,
		Description: "Add to or replace the owner's notes about how they write (for example \"never use usted with customers\"). " +
			"Only when the owner tells you a rule about their style. Logged with the old and new text."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in notesIn) (*mcp.CallToolResult, any, error) {
			if err := d.styleOn(ctx); err != nil {
				return errResult(err), nil, nil
			}
			if err := d.Policy.CheckClaudeChange(ctx, d.actor()); err != nil {
				return errResult(err), nil, nil
			}
			if strings.TrimSpace(in.Text) == "" && in.Mode != "replace" {
				return errResult(errors.New("text is required")), nil, nil
			}
			mode := strings.ToLower(strings.TrimSpace(in.Mode))
			if mode != "" && mode != "append" && mode != "replace" {
				return errResult(errors.New("mode must be append or replace")), nil, nil
			}
			old, now, err := d.Style.SetNotes(ctx, in.Text, mode != "replace", d.actor())
			if err != nil {
				return errResult(err), nil, nil
			}
			d.Policy.Audit(ctx, d.actor(), "style.notes", "", "ok", map[string]any{"old": old, "new": now})
			return text("Notes saved. They now read:\n" + now), nil, nil
		})
}

func styleText(st *style.State) string { return style.ForClaude(st) }
