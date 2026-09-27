package api

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"panel/models"
)

// Admin alerts on upstream DOWN / recovery. Delivered to Telegram when
// TELEGRAM_BOT_TOKEN + TELEGRAM_CHAT_ID are configured; every alert is also
// written to the panel log regardless, so nothing is lost silently.

func (s *Server) alertDown(u models.Upstream, probeErr, switched string) {
	msg := fmt.Sprintf("🔴 Прокси %s (%s:%d) DOWN — упал при проверке %s: %s",
		u.Name, u.Host, u.Port, joinQuoted(s.cfg.HealthcheckTargets), truncStr(probeErr, 400))
	switch s.cfg.HealthcheckMode {
	case "auto":
		if switched != "" {
			msg += ". Переключили пользователей на резерв: " + switched
		} else {
			msg += ". Резервного прокси нет — пользователи остаются на прежнем маршруте"
		}
	default: // monitor
		msg += ". Режим monitor: автоматическое переключение выключено"
	}
	s.notify(msg)
}

func (s *Server) alertUp(u models.Upstream) {
	msg := fmt.Sprintf("🟢 Прокси %s (%s:%d) восстановился — проверка %s проходит. "+
		"В балансировочные группы вернётся автоматически; sticky-листенеры, переведённые на резерв, остаются на нём — ротация через API.",
		u.Name, u.Host, u.Port, joinQuoted(s.cfg.HealthcheckTargets))
	s.notify(msg)
}

func joinQuoted(items []string) string {
	out := ""
	for i, it := range items {
		if i > 0 {
			out += ", "
		}
		out += it
	}
	return out
}

// notify sends text to Telegram (when configured) and always logs it.
func (s *Server) notify(text string) {
	log.Printf("[ALERT] %s", text)
	if s.cfg.TelegramBotToken == "" || s.cfg.TelegramChatID == "" {
		return
	}
	go func(token, chat, text string) {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.PostForm("https://api.telegram.org/bot"+token+"/sendMessage",
			url.Values{"chat_id": {chat}, "text": {text}})
		if err != nil {
			log.Printf("[ALERT] telegram: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
			log.Printf("[ALERT] telegram status %d: %s", resp.StatusCode, string(b))
		}
	}(s.cfg.TelegramBotToken, s.cfg.TelegramChatID, text)
}
