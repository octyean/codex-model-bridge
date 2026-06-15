package adapters

import (
	"strings"
	"testing"
)

func TestNormalizePatchInputExtractsJSONEnvelope(t *testing.T) {
	got := NormalizePatchInput(`{"input":"*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch\n"}`)
	want := "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputPrefixesBareUpdateContextLines(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: README.md\n@@\n# Demo\n\n+## Usage\n+\n+npm run dev\n+\n## Install\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: README.md\n@@\n # Demo\n \n+## Usage\n+\n+npm run dev\n+\n ## Install\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputPrefixesBareReplacementContextLines(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: service.go\n@@\npackage demo\n\n+// status returns the current readiness state of the service.\n func status() string {\n-  return \"pending\"\n+  return \"ready\"\n }\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: service.go\n@@\n package demo\n \n+// status returns the current readiness state of the service.\n func status() string {\n-  return \"pending\"\n+  return \"ready\"\n }\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputDoesNotTouchAddFileContent(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Add File: src/utils/format.ts\n+export const formatName = (name: string) => name.trim();\n*** End Patch"))
	want := "*** Begin Patch\n*** Add File: src/utils/format.ts\n+export const formatName = (name: string) => name.trim();\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputRepairsEndPatchAsAddedLine(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Add File: deep/path/notes.md\n+# Notes\n+\n+## Purpose\n+\n+轻量级的项目随记入口。\n+ +*** End Patch"))
	want := "*** Begin Patch\n*** Add File: deep/path/notes.md\n+# Notes\n+\n+## Purpose\n+\n+轻量级的项目随记入口。\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputMergesRepeatedPatchEnvelopes(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: guide.md\n@@\n ## Troubleshooting\n+## Configuration\n*** End Patch\n*** Begin Patch\n*** Update File: guide.md\n@@\n-4. Verify health checks and tool routing.\n+4. 验证健康检查、模型路由和 apply_patch 工具调用。\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: guide.md\n@@\n ## Troubleshooting\n+## Configuration\n*** Update File: guide.md\n@@\n-4. Verify health checks and tool routing.\n+4. 验证健康检查、模型路由和 apply_patch 工具调用。\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestNormalizePatchBoundaryLines(t *testing.T) {
	got := normalizePatchBoundaryLines(" +*** Begin Patch\n  +*** End Patch\n")
	want := "*** Begin Patch\n*** End Patch\n"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputDropsTrailingTextAfterEndOfFile(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: README.md\n@@\n# Demo\n+## Usage\n+\n+npm run dev\n+\n+Open http://localhost:5173 in your browser.\n+\n*** End of File\n## Usage\n\nnpm run dev\n\nOpen http://localhost:5173 in your browser.\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: README.md\n@@\n # Demo\n+## Usage\n+\n+npm run dev\n+\n+Open http://localhost:5173 in your browser.\n+\n*** End of File\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputDropsNoOpTrailingUpdateHunk(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: README.md\n@@\n# Demo\n+ \n+ ## Usage\n+ \n+ npm run dev\n \n ## Install\n@@\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: README.md\n@@\n # Demo\n+ \n+ ## Usage\n+ \n+ npm run dev\n \n ## Install\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputDropsContextOnlyTrailingUpdateHunk(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: README.md\n@@\n# Demo\n\n+## Usage\n+\n+npm run dev\n+\n## Install\n@@\n## Install\n\nnpm install\n## Install\n\nnpm install\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: README.md\n@@\n # Demo\n \n+## Usage\n+\n+npm run dev\n+\n ## Install\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputKeepsMutatingHunkWithBadContext(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: service.go\n@@\nfunc status() string {\n}\n+// status returns the current service readiness.\n+func status() string {\n+  return \"ready\"\n+}\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: service.go\n@@\n func status() string {\n }\n+// status returns the current service readiness.\n+func status() string {\n+  return \"ready\"\n+}\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputRepairsMarkdownListContext(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: guide.md\n@@\n## Prerequisites\n\n- Node.js 20+\n- A valid API key\n- Network access to the upstream provider\n+## Configuration\n+\n+- `CODEX_BRIDGE_LOG_LEVEL` — Log level\n+\n@@\n-4. Verify health checks and tool routing.\n+4. 验证健康检查、模型路由和 apply_patch 工具调用。\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: guide.md\n@@\n ## Prerequisites\n \n - Node.js 20+\n - A valid API key\n - Network access to the upstream provider\n+## Configuration\n+\n+- `CODEX_BRIDGE_LOG_LEVEL` — Log level\n+\n@@\n-4. Verify health checks and tool routing.\n+4. 验证健康检查、模型路由和 apply_patch 工具调用。\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputKeepsExplicitMarkdownListDeletion(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: guide.md\n@@\n-- old item\n+- new item\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: guide.md\n@@\n-- old item\n+- new item\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestNormalizePatchInputExtractsNestedJSONEnvelope(t *testing.T) {
	got := NormalizePatchInput("{\"arguments\":{\"patch\":\"```patch\\n*** Begin Patch\\n*** Add File: hello.txt\\n+hello\\n*** End Patch\\n```\"}}")
	want := "*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestNormalizePatchInputPreservesHunkWhitespace(t *testing.T) {
	input := "  *** Begin Patch\n*** Update File: a.vue\n@@\n- \t<view>\n+ \t<view class=\"x\">\n  \t  <text>hi</text>\n*** End Patch\n  "
	got := NormalizePatchInput(input)
	if strings.Contains(got, "\r") {
		t.Fatalf("normalized should use LF: %q", got)
	}
	for _, want := range []string{
		"- \t<view>",
		"+ \t<view class=\"x\">",
		"  \t  <text>hi</text>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized lost whitespace %q in %q", want, got)
		}
	}
}

func TestNormalizePatchInputCompletesMissingEnvelope(t *testing.T) {
	got := NormalizePatchInput("*** Update File: a.txt\n@@\n-old\n+new")
	want := "*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestNormalizePatchInputCompletesMissingEnd(t *testing.T) {
	got := NormalizePatchInput("*** Begin Patch\n*** Add File: a.txt\n+hello")
	want := "*** Begin Patch\n*** Add File: a.txt\n+hello\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputRepairsDuplicatedReplacementHunk(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: a.vue\n@@\n    <text>{{ value }}</text>\n+    <text>{{ value }} ok</text>\n-    <text>{{ value }}</text>\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: a.vue\n@@\n-    <text>{{ value }}</text>\n+    <text>{{ value }} ok</text>\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputRepairsInsertionLikeReplacementHunk(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: a.vue\n@@\n    <text>{{ comboOptionMeta(item) }}</text>\n+    <text>{{ comboOptionMeta(item) }} · 已优化</text>\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: a.vue\n@@\n-   <text>{{ comboOptionMeta(item) }}</text>\n+    <text>{{ comboOptionMeta(item) }} · 已优化</text>\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestNormalizePatchInputKeepsAppendInsertion(t *testing.T) {
	got := NormalizePatchInput("*** Begin Patch\n*** Update File: .env.example\n@@\n API_URL=http://localhost\n+ENABLE_LOG=true\n*** End Patch")
	want := "*** Begin Patch\n*** Update File: .env.example\n@@\n API_URL=http://localhost\n+ENABLE_LOG=true\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputKeepsAppendInsertion(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: .env.example\n@@\n API_URL=http://localhost\n+ENABLE_LOG=true\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: .env.example\n@@\n API_URL=http://localhost\n+ENABLE_LOG=true\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputRemovesInvalidAddFileHunkHeader(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Add File: src/utils/format.ts\n@@\n+export const formatName = (name: string) => name.trim();\n*** End Patch"))
	want := "*** Begin Patch\n*** Add File: src/utils/format.ts\n+export const formatName = (name: string) => name.trim();\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestRepairDeepSeekPatchInputDropsHallucinatedContextForSingleReplacement(t *testing.T) {
	got := RepairDeepSeekPatchInput(NormalizePatchInput("*** Begin Patch\n*** Update File: a.vue\n@@\n    <text class=\"combo-option__name\">{{ item.itemProductName || '暂无名称' }}</text>\n-    <text class=\"combo-option__meta\">{{ comboOptionMeta(item) }}</text>\n+    <text class=\"combo-option__meta\">{{ comboOptionMeta(item) }} · 已优化</text>\n  </view>\n*** End Patch"))
	want := "*** Begin Patch\n*** Update File: a.vue\n@@\n-    <text class=\"combo-option__meta\">{{ comboOptionMeta(item) }}</text>\n+    <text class=\"combo-option__meta\">{{ comboOptionMeta(item) }} · 已优化</text>\n*** End Patch"
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
	}
}

func TestClassifyPatchFailure(t *testing.T) {
	cases := map[string]PatchFailureKind{
		"apply_patch verification failed: Failed to find expected lines": PatchFailureContextMismatch,
		"Failed to find context":                 PatchFailureContextMismatch,
		"permission denied":                      PatchFailurePermissionOrSandbox,
		"sandbox denied":                         PatchFailurePermissionOrSandbox,
		"open foo: no such file":                 PatchFailurePathError,
		"invalid hunk at line 3":                 PatchFailureInvalidHunk,
		"invalid patch: missing *** Begin Patch": PatchFailureMalformedPatch,
		"apply_patch failed unexpectedly":        PatchFailureUnknown,
		"ok":                                     PatchFailureNone,
	}
	for output, want := range cases {
		if got := ClassifyPatchFailure(output); got != want {
			t.Fatalf("ClassifyPatchFailure(%q) = %q, want %q", output, got, want)
		}
	}
}
