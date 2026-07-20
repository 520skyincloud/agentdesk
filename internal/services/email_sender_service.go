package services

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"agent-desk/internal/pkg/config"
	"agent-desk/internal/pkg/errorsx"
)

type emailSender interface {
	SendVerificationCode(ctx context.Context, to, code, purpose string) error
}

type smtpEmailSender struct{}

func (smtpEmailSender) SendVerificationCode(ctx context.Context, to, code, purpose string) error {
	cfg := config.Current().Email
	if !cfg.Enabled {
		return errorsx.InvalidParam("邮箱验证码服务尚未配置")
	}
	host := strings.TrimSpace(cfg.Host)
	from := strings.TrimSpace(cfg.From)
	if host == "" || from == "" {
		return errorsx.InvalidParam("邮箱验证码服务缺少 SMTP 地址或发件邮箱")
	}
	if _, err := mail.ParseAddress(to); err != nil {
		return errorsx.InvalidParam("邮箱格式不正确")
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return errorsx.InvalidParam("发件邮箱配置不正确")
	}

	subject := "登录验证码"
	if purpose == EmailVerificationPurposeRemoteSetup {
		subject = "企微员工号绑定验证码"
	}
	body := fmt.Sprintf("您的验证码是 %s，10 分钟内有效。请勿将验证码告诉他人。", code)
	message := buildSMTPMessage(cfg, to, subject, body)
	return sendSMTPMessage(ctx, cfg, to, message)
}

func buildSMTPMessage(cfg config.EmailConfig, to, subject, body string) []byte {
	from := mail.Address{Name: strings.TrimSpace(cfg.FromName), Address: strings.TrimSpace(cfg.From)}
	headers := []string{
		"From: " + from.String(),
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("UTF-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + body + "\r\n")
}

func sendSMTPMessage(ctx context.Context, cfg config.EmailConfig, to string, message []byte) error {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	address := cfg.Address()
	mode := strings.ToLower(strings.TrimSpace(cfg.TLSMode))
	if mode == "" {
		mode = "starttls"
	}

	var (
		conn net.Conn
		err  error
	)
	if mode == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: strings.TrimSpace(cfg.Host)})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("连接邮件服务器失败: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, strings.TrimSpace(cfg.Host))
	if err != nil {
		return fmt.Errorf("初始化邮件服务器连接失败: %w", err)
	}
	defer client.Close()
	if mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("邮件服务器不支持 STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: strings.TrimSpace(cfg.Host)}); err != nil {
			return fmt.Errorf("邮件服务器 TLS 握手失败: %w", err)
		}
	} else if mode != "tls" && mode != "plain" {
		return errorsx.InvalidParam("邮件 TLS 模式仅支持 starttls、tls 或 plain")
	}
	if username := strings.TrimSpace(cfg.Username); username != "" {
		if err := client.Auth(smtp.PlainAuth("", username, cfg.Password, strings.TrimSpace(cfg.Host))); err != nil {
			return fmt.Errorf("邮件服务器认证失败: %w", err)
		}
	}
	if err := client.Mail(strings.TrimSpace(cfg.From)); err != nil {
		return fmt.Errorf("设置发件人失败: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("设置收件人失败: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("创建邮件正文失败: %w", err)
	}
	writer := bufio.NewWriter(wc)
	if _, err := writer.Write(message); err != nil {
		_ = wc.Close()
		return fmt.Errorf("发送邮件正文失败: %w", err)
	}
	if err := writer.Flush(); err != nil {
		_ = wc.Close()
		return fmt.Errorf("发送邮件正文失败: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("提交邮件失败: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("结束邮件会话失败: %w", err)
	}
	return nil
}
