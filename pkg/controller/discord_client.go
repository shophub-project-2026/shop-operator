package controller

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// DiscordClient abstracts the Discord REST operations the operator needs to
// materialise a per-shop notification channel. It is an interface so the
// reconciler can be unit-tested with a fake; the production implementation is
// discordgoClient, backed by github.com/bwmarrin/discordgo.
type DiscordClient interface {
	// EnsureChannel returns the ID of a text channel named `name` in the guild,
	// creating it if it does not already exist. It is idempotent: calling it
	// repeatedly with the same name returns the same channel.
	EnsureChannel(guildID, name string) (channelID string, err error)
	// EnsureWebhook returns the ID and full URL of a webhook named `name` on the
	// channel, creating it if absent. It is idempotent on the webhook name.
	EnsureWebhook(channelID, name string) (webhookID, webhookURL string, err error)
	// DeleteChannel removes a channel by ID. Deleting a missing channel is not an
	// error, so the finalizer can run safely more than once.
	DeleteChannel(channelID string) error
}

// discordgoClient is the production DiscordClient. The bot token must belong to
// a bot invited to the target guild with the Manage Channels and Manage
// Webhooks permissions. Only the REST API is used, so the gateway websocket is
// never opened.
type discordgoClient struct {
	session *discordgo.Session
}

// NewDiscordGoClient builds a DiscordClient from a bot token. The token is the
// raw token; the "Bot " prefix is added here.
func NewDiscordGoClient(botToken string) (DiscordClient, error) {
	s, err := discordgo.New("Bot " + botToken)
	if err != nil {
		return nil, fmt.Errorf("create discord session: %w", err)
	}
	return &discordgoClient{session: s}, nil
}

func (c *discordgoClient) EnsureChannel(guildID, name string) (string, error) {
	name = sanitizeChannelName(name)

	channels, err := c.session.GuildChannels(guildID)
	if err != nil {
		return "", fmt.Errorf("list guild channels: %w", err)
	}
	for _, ch := range channels {
		if ch.Type == discordgo.ChannelTypeGuildText && ch.Name == name {
			return ch.ID, nil
		}
	}

	created, err := c.session.GuildChannelCreate(guildID, name, discordgo.ChannelTypeGuildText)
	if err != nil {
		return "", fmt.Errorf("create channel %q: %w", name, err)
	}
	return created.ID, nil
}

func (c *discordgoClient) EnsureWebhook(channelID, name string) (string, string, error) {
	hooks, err := c.session.ChannelWebhooks(channelID)
	if err != nil {
		return "", "", fmt.Errorf("list channel webhooks: %w", err)
	}
	for _, h := range hooks {
		if h.Name == name {
			return h.ID, webhookURL(h.ID, h.Token), nil
		}
	}

	h, err := c.session.WebhookCreate(channelID, name, "")
	if err != nil {
		return "", "", fmt.Errorf("create webhook %q: %w", name, err)
	}
	return h.ID, webhookURL(h.ID, h.Token), nil
}

func (c *discordgoClient) DeleteChannel(channelID string) error {
	_, err := c.session.ChannelDelete(channelID)
	if err != nil {
		// A 404 means the channel is already gone — treat as success so the
		// finalizer does not block deletion of the CR.
		if rest, ok := err.(*discordgo.RESTError); ok && rest.Response != nil && rest.Response.StatusCode == 404 {
			return nil
		}
		return fmt.Errorf("delete channel %s: %w", channelID, err)
	}
	return nil
}

func webhookURL(id, token string) string {
	return fmt.Sprintf("https://discord.com/api/webhooks/%s/%s", id, token)
}

// sanitizeChannelName maps a shop name to a Discord-safe text-channel name:
// lowercase, spaces/underscores to hyphens. Discord itself normalises names,
// but doing it here keeps idempotent name matching exact.
func sanitizeChannelName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ReplaceAll(name, "_", "-")
	return name
}
