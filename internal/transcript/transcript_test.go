package transcript

import (
	"strings"
	"testing"
)

func TestVisibleProgressNoteKeepsBriefProgressWithToolCalls(t *testing.T) {
	if !strings.Contains(visibleProgressNote, "include one brief user-visible progress sentence together with the tool calls") {
		t.Fatalf("visible progress note must keep brief progress text with tool calls")
	}
	if !strings.Contains(visibleProgressNote, "If you cannot include progress text and tool calls together, omit the text and call the tools directly") {
		t.Fatalf("visible progress note must prefer tool calls when progress text would replace them")
	}
	if !strings.Contains(visibleProgressNote, "Do not use it as scratchpad, self-debate, implementation analysis, or a standalone plan") {
		t.Fatalf("visible progress note must forbid plan-only assistant content before tool work")
	}
	if !strings.Contains(visibleProgressNote, "A content-only assistant response is only appropriate when the task is complete or blocked and needs user input") {
		t.Fatalf("visible progress note must reserve content-only responses for final or blocked states")
	}
	if strings.Contains(visibleProgressNote, "CHAT_RESPONSE_STATE") {
		t.Fatalf("visible progress note must not require private response markers")
	}
}
