package review

import (
	"fmt"
	"strings"

	"github.com/Duops/SherlockOps/internal/domain"
)

func systemPrompt(language string) string {
	if language == "ru" {
		return `Ты опытный SRE, который борется с шумом алертов. Тебе дают статистику уведомлений
Alertmanager за период. Твоя задача — объяснить, кто генерирует объём, и дать конкретные
действия, которые уменьшат количество уведомлений без потери важных сигналов.

Формат ответа (markdown, кратко, без вступлений):
1. **Итог** — 2-3 предложения: сколько шума, откуда, сколько можно убрать.
2. **Основные источники шума** — сгруппируй топ-алерты по причине (флапающий таргет,
   слишком низкий порог, нехватка ресурсов, дубли между окружениями, алерт без действия).
3. **Рекомендации** — список по убыванию эффекта. Для каждого алерта: что именно сделать —
   поднять порог (с примерным значением), увеличить "for", отключить или перевести в info,
   добавить inhibit/group_by в Alertmanager, поднять ресурсы/место, починить первопричину.
   Указывай окружение, если проблема локальна.
4. **Ожидаемый эффект** — на сколько процентов упадёт объём, если сделать топ-3 пункта.

Не выдумывай данных, которых нет в статистике. Если причину нельзя определить по названию
алерта и цифрам, напиши, что нужно проверить.`
	}
	return `You are an experienced SRE fighting alert fatigue. You are given Alertmanager
notification statistics for a period. Explain what generates the volume and give concrete
actions that reduce notifications without losing important signals.

Answer format (markdown, concise, no preamble):
1. **Summary** — 2-3 sentences: how much noise, where from, how much can be removed.
2. **Main noise sources** — group the top alerts by cause (flapping target, threshold too
   low, resource exhaustion, duplicates across environments, alert with no action).
3. **Recommendations** — list ordered by impact. For each alert: exactly what to do — raise
   the threshold (with an approximate value), increase "for", disable or downgrade to info,
   add inhibit/group_by in Alertmanager, add resources/disk, fix the root cause. Name the
   environment when the problem is local.
4. **Expected effect** — how many percent the volume drops if the top 3 items are done.

Do not invent data absent from the statistics. If a cause cannot be determined from the
alert name and numbers, say what should be checked.`
}

// BuildPrompt renders the statistics as the user message for the LLM.
func BuildPrompt(s *domain.AlertStats, language string) string {
	var b strings.Builder
	env := s.Environment
	if env == "" {
		env = "all environments"
	}
	if language == "ru" {
		fmt.Fprintf(&b, "Период: %d дней (%s — %s), окружение: %s\n", s.Days,
			s.Since.Format("2006-01-02"), s.Until.Format("2006-01-02"), env)
		fmt.Fprintf(&b, "Всего уведомлений: %d (firing %d, resolved %d), ≈%.0f в день, уникальных алертов: %d\n",
			s.Total, s.Firing, s.Resolved, s.PerDay, s.UniqueAlerts)
		fmt.Fprintf(&b, "Доля топ-3: %.0f%%, топ-8: %.0f%%, топ-30: %.0f%%\n\n", s.Top3Share, s.Top8Share, s.Top30Share)
		b.WriteString("Топ алертов (имя | окружения | всего | firing | resolved | доля):\n")
	} else {
		fmt.Fprintf(&b, "Period: %d days (%s — %s), environment: %s\n", s.Days,
			s.Since.Format("2006-01-02"), s.Until.Format("2006-01-02"), env)
		fmt.Fprintf(&b, "Total notifications: %d (firing %d, resolved %d), ≈%.0f per day, unique alerts: %d\n",
			s.Total, s.Firing, s.Resolved, s.PerDay, s.UniqueAlerts)
		fmt.Fprintf(&b, "Top-3 share: %.0f%%, top-8: %.0f%%, top-30: %.0f%%\n\n", s.Top3Share, s.Top8Share, s.Top30Share)
		b.WriteString("Top alerts (name | environments | total | firing | resolved | share):\n")
	}
	for i, a := range s.TopAlerts {
		if i >= 30 {
			break
		}
		fmt.Fprintf(&b, "%2d. %s | %s | %d | %d | %d | %.1f%%\n", i+1, a.Name,
			strings.Join(a.Environments, ","), a.Count, a.Firing, a.Resolved, a.Share)
	}
	if len(s.ByEnvironment) > 1 {
		if language == "ru" {
			b.WriteString("\nПо окружениям (окружение | всего | уникальных | доля | топ-алерт):\n")
		} else {
			b.WriteString("\nBy environment (environment | total | unique | share | top alert):\n")
		}
		for _, e := range s.ByEnvironment {
			fmt.Fprintf(&b, "- %s | %d | %d | %.1f%% | %s\n", e.Environment, e.Count, e.UniqueAlerts, e.Share, e.TopAlert)
		}
	}
	return b.String()
}
