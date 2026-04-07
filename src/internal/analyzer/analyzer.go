package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/Duops/SherlockOps/internal/domain"
)

const defaultSystemPromptEN = `You are a DevOps on-call agent. Language: English only.

TOOLS:
You have access to infrastructure tools (Prometheus/VictoriaMetrics, Loki, Kubernetes, cloud APIs, databases).
Use them to investigate the alert — do not guess, only report what the tools return.

LABELS IN QUERIES:
Before building any query, analyze the alert labels carefully.
Use ONLY labels that are actually present in the alert. Do not invent labels.
Pick labels that identify the specific resource for the metric.

RULES:
- Do not fabricate data — only use what tools return.
- If a tool returns empty 2 times in a row — stop trying that direction.
- NEVER call the same tool with the same params more than 2 times.
- If a tool returns an error — move on, do not retry endlessly.
- "Too many points" or similar range errors — switch to instant query or narrow the range. Do NOT retry the same call.
- Container orchestration: if label selector returns empty — try get by name from alert Labels.

INVESTIGATION PLAN:
Use available tools in this priority order. Skip categories you have no tools for.
1. METRICS — query metrics related to the alert (restarts, memory, cpu, disk, queue depth, custom metrics from labels)
2. ORCHESTRATION — if container/pod alert → get resource status OR logs (one of the two, not both at first)
3. LOGS — only if step 2 did not reveal the cause, or if no orchestration tools available
4. INFRASTRUCTURE — cloud/VM/managed service status, database state, message broker health
5. After {TOOL_CALL_LIMIT}+ tool calls — write your answer with what you have

Each step: if you found the root cause → skip remaining steps and write the answer.

STOP CRITERIA (CRITICAL):
Write your final answer IMMEDIATELY when ANY of these is true:
- Log contains "Killed", "OOMKilled", "Error", a specific exception → root cause found, STOP
- Metrics show the problem clearly (restarts > 0, disk full, etc.) and you know the reason → STOP
- You can answer "why is the alert firing?" with concrete actionable steps → STOP
- {TOOL_CALL_LIMIT}+ tool calls made → STOP, write answer with available data
- Do NOT make "verification" calls if the cause is already clear
- Do NOT query more metrics if logs already show the crash reason
- Do NOT fetch resource details if the cause is already clear from logs or metrics

RESPONSE FORMAT (the messenger adds header and footer automatically):

Severity: first line starts with severity emoji.
  Determine from alert severity label: critical → 🔴, warning → 🟡, otherwise → 🟢.
  Escalate if root cause is worse than label (e.g., warning but OOMKilled with hundreds of restarts → 🔴).
  Never downgrade severity.

Required sections (in this exact order):

🔴/🟡/🟢 *Diagnosis:* <root cause in one sentence>

📊 *Findings:* <1-2 lines explaining what you found>
` + "`" + `<key numbers separated by | — restarts, exitCode, limit, log snippet>` + "`" + `

🛠️ *Actions:*
1️⃣ <action>
2️⃣ <action>
3️⃣ <action>

Do NOT add a tool trace line — the messenger adds it automatically.

Formatting: *bold* = single asterisk. Italics = _text_.
FORBIDDEN: **, ##, ---

BOT COMMANDS:
When <user_command> is present — it's a user request in an alert thread.
Understand free-form speech:
- "silence 2h", "mute for 4 hours" → create silence (duration from text or 2h default)
- "unsilence", "unmute" → remove active silence
- "reanalyze", "check again" → full analysis (standard protocol)
- Any other text → analyze alert and answer the specific question`

const defaultSystemPromptRU = `Ты — DevOps on-call агент. Язык: ТОЛЬКО русский.

ИНСТРУМЕНТЫ:
У тебя есть доступ к инструментам инфраструктуры (Prometheus/VictoriaMetrics, Loki, Kubernetes, облачные API, базы данных).
Используй их для расследования алерта — не выдумывай данные, используй только то, что вернули инструменты.

ЛЕЙБЛЫ В ЗАПРОСАХ:
ПЕРЕД построением запроса — проанализируй Labels алерта.
Используй в запросах ТОЛЬКО те лейблы, которые РЕАЛЬНО присутствуют в алерте.
Не добавляй лейблы, которых нет. Выбирай лейблы, которые идентифицируют конкретный ресурс.

ПРАВИЛА:
- Не выдумывать данные — только то что вернули инструменты.
- Если инструмент вернул пустой ответ 2 раза подряд — прекращай попытки по этому направлению.
- ЗАПРЕЩЕНО вызывать один и тот же инструмент с одинаковыми параметрами более 2 раз.
- Если инструмент вернул ошибку — двигайся дальше, не повторяй бесконечно.
- "Too many points" или аналогичная ошибка = слишком широкий диапазон. Переключись на instant-запрос или сузь диапазон. НЕ повторяй тот же вызов.
- Оркестрация: если label selector вернул пусто — попробуй get ресурс по имени из Labels алерта.

ПЛАН РАССЛЕДОВАНИЯ:
Используй доступные инструменты в порядке приоритета. Пропускай категории, для которых нет инструментов.
1. МЕТРИКИ — запроси метрики по алерту (restarts, memory, cpu, disk, глубина очереди, custom metrics из лейблов)
2. ОРКЕСТРАЦИЯ — если алерт на контейнер/под → статус ресурса ИЛИ логи (одно из двух, не оба сразу)
3. ЛОГИ — только если шаг 2 не выявил причину, или инструментов оркестрации нет
4. ИНФРАСТРУКТУРА — статус облака/ВМ/managed-сервиса, состояние БД, здоровье брокера сообщений
5. После {TOOL_CALL_LIMIT}+ вызовов — писать ответ с тем что есть

Каждый шаг: если нашёл root cause → пропусти остальные шаги и пиши ответ.

СТОП-КРИТЕРИИ (КРИТИЧЕСКИ ВАЖНО):
НЕМЕДЛЕННО пиши финальный ответ когда выполнено ЛЮБОЕ из условий:
- Лог содержит "Killed", "OOMKilled", "Error", конкретную ошибку → причина найдена, СТОП
- Метрики показывают проблему (restarts > 0, диск полный и т.д.) и ты знаешь reason → СТОП
- Ты можешь ответить "почему алерт firing?" и написать конкретные шаги → СТОП
- Сделано {TOOL_CALL_LIMIT}+ вызовов → СТОП, пиши с тем что есть
- НЕ делай "проверок для уверенности", если причина уже ясна
- НЕ запрашивай дополнительные метрики если логи уже показали причину краша
- НЕ запрашивай детали ресурсов если причина уже ясна из логов или метрик

ФОРМАТ ОТВЕТА (мессенджер сам добавит header и footer):

Severity: первая строка начинается с эмодзи уровня серьёзности.
  Определение: по label severity из алерта (critical → 🔴, warning → 🟡, иначе → 🟢).
  Повышение: если root cause тяжелее label-а (например, warning но OOMKilled) — повысь до 🔴.
  Понижение ЗАПРЕЩЕНО.

Обязательные секции (именно в таком порядке):

🔴/🟡/🟢 *Диагноз:* <причина в одном предложении>

📊 *Доказательства:* <1-2 строки прозы с объяснением>
` + "`" + `<ключевые цифры через | — restarts, exitCode, limit, лог>` + "`" + `

🛠️ *Что сделать:*
1️⃣ <действие>
2️⃣ <действие>
3️⃣ <действие>

НЕ добавляй трейс инструментов — мессенджер добавит его автоматически.

Разметка: *bold* = один asterisk, курсив = _текст_.
ЗАПРЕЩЕНО: **, ##, ---

@BOT КОМАНДЫ:
Когда в <user_command> есть текст — это запрос пользователя в треде алерта.
Агент понимает свободную речь на русском и английском:
- "silence 2h", "замьюти на 4 часа" → создать silence (duration из текста или 2h по умолчанию)
- "unsilence", "размьюти" → удалить active silence
- "reanalyze", "переанализируй", "посмотри ещё раз" → полный анализ (стандартный протокол)
- Любой другой текст — проанализировать алерт и ответить на конкретный вопрос`

// toolRecord tracks a tool invocation and whether it succeeded.
type toolRecord struct {
	name    string
	success bool
}

// ToolNameResolver maps a tool prefix to its display name from config.
type ToolNameResolver func(prefix string) string

// Analyzer orchestrates LLM-based alert analysis with tool calling.
type Analyzer struct {
	llm              domain.LLMProvider
	tools            domain.ToolExecutor
	runbooks         domain.RunbookMatcher
	systemPrompt     string
	language         string
	maxIterations    int
	inputTokenCost   float64
	outputTokenCost  float64
	nameResolver     ToolNameResolver
	logger           *slog.Logger

	// maxToolOutputChars caps a single tool result's content before it is
	// appended to the LLM message history. 0 = no cap. Defaults to 20000
	// (~5k tokens) in New(). Without this, tools like k8s_get_pods or loki
	// log queries can return hundreds of KB, inflate every subsequent
	// iteration's input, and blow through the context window.
	maxToolOutputChars int

	// contextSoftLimitTokens is a running budget for totalInput+totalOutput
	// tokens. When exceeded, the analyzer stops iterating and emits the
	// best-effort answer built from lastContent rather than surface a 400
	// "prompt too long" error to the user. 0 = no limit. Defaults to
	// 800_000 in New() which leaves headroom below Anthropic's 1M cap.
	contextSoftLimitTokens int

	// keepRecentToolResults controls the compaction window. Before each
	// LLM call, tool results older than the most recent N are replaced
	// with a one-line summary. This bounds context growth across
	// iterations: linear → constant. 0 = no compaction. Defaults to 6.
	keepRecentToolResults int
}

// New creates a new Analyzer.
func New(
	llm domain.LLMProvider,
	tools domain.ToolExecutor,
	systemPrompt, language string,
	maxIterations int,
	logger *slog.Logger,
) *Analyzer {
	if maxIterations <= 0 {
		maxIterations = 10
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Analyzer{
		llm:                    llm,
		tools:                  tools,
		systemPrompt:           systemPrompt,
		language:               language,
		maxIterations:          maxIterations,
		logger:                 logger,
		maxToolOutputChars:     20000,
		contextSoftLimitTokens: 800_000,
		keepRecentToolResults:  6,
	}
}

// SetKeepRecentToolResults overrides how many recent tool results to keep
// fully sized in the message history. Anything older is rewritten into a
// one-line summary on every iteration.
func (a *Analyzer) SetKeepRecentToolResults(n int) {
	a.keepRecentToolResults = n
}

// SetMaxToolOutputChars overrides the per-tool-result truncation cap.
// Pass 0 to disable truncation (not recommended for LLMs with bounded
// context windows).
func (a *Analyzer) SetMaxToolOutputChars(n int) {
	a.maxToolOutputChars = n
}

// SetContextSoftLimitTokens overrides the running input+output token budget
// after which the analyzer stops iterating and returns the best-effort
// answer. Pass 0 to disable.
func (a *Analyzer) SetContextSoftLimitTokens(n int) {
	a.contextSoftLimitTokens = n
}

// SetNameResolver sets a function to resolve tool prefixes to display names.
func (a *Analyzer) SetNameResolver(resolver ToolNameResolver) {
	a.nameResolver = resolver
}

// SetTokenCost sets per-token pricing from config for cost estimation.
func (a *Analyzer) SetTokenCost(inputCost, outputCost float64) {
	a.inputTokenCost = inputCost
	a.outputTokenCost = outputCost
}

// SetRunbookStore attaches a runbook store to the analyzer.
// When set, matching runbooks are injected into the LLM context for each alert.
func (a *Analyzer) SetRunbookStore(store domain.RunbookMatcher) {
	a.runbooks = store
}

// Analyze processes an alert through the LLM with tool calling and returns the result.
func (a *Analyzer) Analyze(ctx context.Context, alert *domain.Alert) (*domain.AnalysisResult, error) {
	// Derive a scoped logger that tags every log line in this analysis with
	// the alert identifiers. This is what lets operators correlate a chain
	// of "tool call" / "sending LLM request" / "tool call OK" lines back to
	// a specific alert in a single grep.
	log := a.logger.With(
		"alert_fingerprint", alert.Fingerprint,
		"alert_name", alert.Name,
		"alert_source", alert.Source,
	)
	if alert.RequestID != "" {
		log = log.With("request_id", alert.RequestID)
	}
	log.Info("analysis started", "max_iterations", a.maxIterations)

	systemPrompt := a.buildSystemPrompt()

	userContent := "<alert>\n" + alert.RawText + "\n</alert>"

	// Inject matching runbooks into the user message.
	if a.runbooks != nil {
		if hasMatch, block := a.runbooks.MatchAlert(alert); hasMatch {
			log.Debug("matched runbooks")
			userContent += "\n\n" + block
			userContent += "\n\nAnalyze the alert using the provided runbooks as context."
		}
	}

	if alert.UserCommand != "" {
		userContent += "\n\nUser request: " + alert.UserCommand
	}

	availableTools, err := a.tools.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	messages := []domain.Message{
		{Role: "user", Content: userContent},
	}

	var toolsUsed []toolRecord
	var lastContent string // track last LLM text for fallback
	var totalTokens, totalInput, totalOutput int
	var model string

	for i := 0; i < a.maxIterations; i++ {
		// Compact older tool results before each LLM call so the message
		// history stays bounded regardless of iteration depth.
		if a.keepRecentToolResults > 0 {
			compacted := compactToolHistory(messages, a.keepRecentToolResults)
			if compacted != 0 {
				log.Debug("compacted tool history",
					"iteration", i+1,
					"compacted", compacted,
					"keep_recent", a.keepRecentToolResults,
				)
			}
		}

		log.Debug("sending LLM request",
			"iteration", i+1,
			"messages", len(messages),
			"tools", len(availableTools),
		)

		// On the last iteration, send without tools to force a text response.
		reqTools := availableTools
		if i == a.maxIterations-1 {
			reqTools = nil
		}

		resp, err := a.llm.Chat(ctx, &domain.ChatRequest{
			SystemPrompt: systemPrompt,
			Messages:     messages,
			Tools:        reqTools,
		})
		if err != nil {
			return nil, fmt.Errorf("LLM chat (iteration %d): %w", i+1, err)
		}

		totalTokens += resp.TokensUsed
		totalInput += resp.InputTokens
		totalOutput += resp.OutputTokens
		if resp.Model != "" {
			model = resp.Model
		}

		if resp.Content != "" {
			lastContent = resp.Content
		}

		// Context soft-limit guard. Stop iterating before we hit the hard
		// provider cap (e.g. Anthropic's 1M tokens) and blow up with a 400.
		// Prefer a best-effort answer over an error surfaced to the user.
		if a.contextSoftLimitTokens > 0 && (totalInput+totalOutput) > a.contextSoftLimitTokens {
			log.Warn("context soft limit reached — returning best-effort result",
				"iteration", i+1,
				"total_input_tokens", totalInput,
				"total_output_tokens", totalOutput,
				"soft_limit", a.contextSoftLimitTokens,
			)
			text := lastContent
			if text == "" {
				text = "Analysis stopped early: LLM context budget exhausted before a final answer was produced. Partial tool results were available but the model did not converge."
			}
			result := buildResult(alert, text, toolsUsed, a.nameResolver)
			result.TotalTokens = totalTokens
			result.InputTokens = totalInput
			result.OutputTokens = totalOutput
			result.Model = model
			result.InputTokenCost = a.inputTokenCost
			result.OutputTokenCost = a.outputTokenCost
			result.Iterations = i + 1
			return result, nil
		}

		if resp.Done || len(resp.ToolCalls) == 0 {
			result := buildResult(alert, resp.Content, toolsUsed, a.nameResolver)
			result.TotalTokens = totalTokens
			result.InputTokens = totalInput
			result.OutputTokens = totalOutput
			result.Model = model
			result.InputTokenCost = a.inputTokenCost
			result.OutputTokenCost = a.outputTokenCost
			result.Iterations = i + 1
			log.Info("analysis completed",
				"iterations", i+1,
				"total_tokens", totalTokens,
				"input_tokens", totalInput,
				"output_tokens", totalOutput,
				"model", model,
			)
			return result, nil
		}

		// Append the assistant message with tool calls.
		messages = append(messages, domain.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call and append results.
		for _, tc := range resp.ToolCalls {
			log.Info("tool call", "tool", tc.Name, "iteration", i+1, "input_keys", toolInputKeys(tc.Input))

			result, execErr := a.tools.Execute(ctx, tc)
			if execErr != nil {
				log.Error("tool call FAILED", "tool", tc.Name, "error", execErr)
				result = &domain.ToolResult{
					CallID:  tc.ID,
					Content: fmt.Sprintf("Error executing tool: %s", execErr.Error()),
					IsError: true,
				}
			} else if result.IsError {
				log.Warn("tool call returned error", "tool", tc.Name, "content_preview", truncateLog(result.Content, 200))
			} else {
				log.Info("tool call OK", "tool", tc.Name, "content_length", len(result.Content))
			}

			toolsUsed = append(toolsUsed, toolRecord{
				name:    tc.Name,
				success: !result.IsError,
			})

			// Cap the tool result before handing it to the LLM. Big blobs
			// (k8s listings, loki chunks, prometheus series) otherwise
			// inflate every subsequent iteration's input and starve the
			// context window.
			capped := capToolContent(result, a.maxToolOutputChars)
			if capped != result {
				log.Debug("tool output truncated before LLM",
					"tool", tc.Name,
					"original_length", len(result.Content),
					"capped_length", len(capped.Content),
				)
			}

			messages = append(messages, domain.Message{
				Role:       "tool",
				ToolResult: capped,
			})
		}
	}

	// Max iterations reached — use last LLM content if available.
	log.Warn("max iterations reached", "max", a.maxIterations)
	text := "Analysis incomplete: maximum iterations reached"
	if lastContent != "" {
		text = lastContent
	}
	result := buildResult(alert, text, toolsUsed, a.nameResolver)
	result.TotalTokens = totalTokens
	result.InputTokens = totalInput
	result.OutputTokens = totalOutput
	result.Model = model
	result.InputTokenCost = a.inputTokenCost
	result.OutputTokenCost = a.outputTokenCost
	result.Iterations = a.maxIterations
	return result, nil
}

func (a *Analyzer) buildSystemPrompt() string {
	prompt := a.systemPrompt
	if prompt == "" {
		if a.language == "ru" {
			prompt = defaultSystemPromptRU
		} else {
			prompt = defaultSystemPromptEN
		}
	}
	return strings.ReplaceAll(prompt, "{TOOL_CALL_LIMIT}", fmt.Sprintf("%d", a.maxIterations))
}

// buildResult constructs the final AnalysisResult with deduplicated tool names.
func toolInputKeys(input map[string]interface{}) []string {
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	return keys
}

func truncateLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func buildResult(alert *domain.Alert, text string, tools []toolRecord, resolver ToolNameResolver) *domain.AnalysisResult {
	seen := make(map[string]bool)
	var names []string
	// Track best status per tool (success if ANY call succeeded).
	status := make(map[string]bool)
	for _, t := range tools {
		if !seen[t.name] {
			seen[t.name] = true
			names = append(names, t.name)
		}
		if t.success {
			status[t.name] = true
		}
	}

	// Build trace grouped by tool category (prometheus, k8s, loki, etc.)
	type catInfo struct {
		success bool
		count   int
	}
	categories := make(map[string]*catInfo)
	for _, t := range tools {
		cat := toolCategory(t.name)
		ci, exists := categories[cat]
		if !exists {
			ci = &catInfo{}
			categories[cat] = ci
		}
		ci.count++
		if t.success {
			ci.success = true
		}
	}

	// Sort categories for stable trace order.
	catKeys := make([]string, 0, len(categories))
	for cat := range categories {
		catKeys = append(catKeys, cat)
	}
	sort.Strings(catKeys)

	var trace []domain.ToolTraceEntry
	for _, cat := range catKeys {
		ci := categories[cat]
		displayName := cat
		if resolver != nil {
			displayName = resolver(cat)
		}
		trace = append(trace, domain.ToolTraceEntry{Name: displayName, Success: ci.success, CallCount: ci.count})
	}

	return &domain.AnalysisResult{
		AlertFingerprint: alert.Fingerprint,
		AlertName:        alert.Name,
		Source:           alert.Source,
		Severity:         string(alert.Severity),
		Text:             text,
		ToolsUsed:        names,
		ToolsTrace:       trace,
	}
}

// compactToolHistory rewrites tool-result messages older than the most
// recent `keep` into a one-line summary, in-place on the messages slice.
// Returns the number of messages that were compacted during this pass.
// Messages that are already compact-marked (content begins with the
// sentinel prefix) are left untouched so we don't re-summarize over and
// over between iterations.
//
// This is what keeps context size constant across long tool-calling
// loops: iteration N sees at most `keep` raw tool payloads plus a handful
// of short summary lines for the older ones.
func compactToolHistory(messages []domain.Message, keep int) int {
	if keep <= 0 {
		return 0
	}
	// Walk backwards and count full-size tool results; compact anything
	// beyond the keep-window.
	toolSeen := 0
	compacted := 0
	for i := len(messages) - 1; i >= 0; i-- {
		m := &messages[i]
		if m.Role != "tool" || m.ToolResult == nil {
			continue
		}
		toolSeen++
		if toolSeen <= keep {
			continue
		}
		if strings.HasPrefix(m.ToolResult.Content, compactedMarker) {
			continue // already compacted
		}
		summary := compactedMarker + fmt.Sprintf("earlier tool result (%d chars) dropped to save context",
			len(m.ToolResult.Content))
		m.ToolResult = &domain.ToolResult{
			CallID:  m.ToolResult.CallID,
			Content: summary,
			IsError: m.ToolResult.IsError,
		}
		compacted++
	}
	return compacted
}

// compactedMarker prefixes tool messages that have been summarized so we
// don't re-compact them on subsequent iterations.
const compactedMarker = "[compacted] "

// capToolContent returns a ToolResult whose Content is clipped to max chars.
// Tools that exceed the cap get a suffix marker telling the LLM how many
// bytes were dropped, so it can ask for a narrower query next time instead
// of silently getting wrong data. Nil / zero max / content under the cap
// are passed through unchanged.
func capToolContent(result *domain.ToolResult, max int) *domain.ToolResult {
	if result == nil || max <= 0 || len(result.Content) <= max {
		return result
	}
	dropped := len(result.Content) - max
	capped := *result
	capped.Content = result.Content[:max] +
		fmt.Sprintf("\n\n…[truncated %d of %d chars — narrow your query (shorter time range, smaller label set, specific resource name) to see the rest]", dropped, len(result.Content))
	return &capped
}

// toolCategory extracts the prefix from a tool name.
// e.g., "prometheus_query" → "prometheus", "k8s_get_pods" → "k8s", "loki_query" → "loki"
func toolCategory(name string) string {
	for i, c := range name {
		if c == '_' {
			return name[:i]
		}
	}
	return name
}
