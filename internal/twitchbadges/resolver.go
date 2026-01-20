package twitchbadges

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/you/gnasty-chat/internal/core"
)

const defaultTTL = 6 * time.Hour

var (
	helixBaseURL     = "https://api.twitch.tv/helix"
	oauthTokenURL    = "https://id.twitch.tv/oauth2/token"
	badgeGlobalPath  = "/chat/badges/global"
	badgeChannelPath = "/chat/badges"
	usersPath        = "/users"
)

type Resolver struct {
	ClientID     string
	ClientSecret string
	HTTP         *http.Client
	TTL          time.Duration

	mu        sync.Mutex
	token     cachedToken
	badgeSets map[string]cacheEntry
	users     map[string]cacheEntry

	enriched sync.Map
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

type cacheEntry struct {
	value     any
	expiresAt time.Time
}

type helixBadgeResponse struct {
	Data []helixBadgeSet `json:"data"`
}

type helixBadgeSet struct {
	SetID    string           `json:"set_id"`
	Versions []helixBadgeItem `json:"versions"`
}

type helixBadgeItem struct {
	ID         string `json:"id"`
	ImageURL1x string `json:"image_url_1x"`
	ImageURL2x string `json:"image_url_2x"`
	ImageURL4x string `json:"image_url_4x"`
}

type helixUsersResponse struct {
	Data []helixUser `json:"data"`
}

type helixUser struct {
	ID string `json:"id"`
}

func NewResolver(clientID, clientSecret string) *Resolver {
	return &Resolver{ClientID: clientID, ClientSecret: clientSecret}
}

func (r *Resolver) Enrich(ctx context.Context, channel string, badges []core.ChatBadge) []core.ChatBadge {
	if r == nil {
		return badges
	}

	channel = strings.TrimSpace(channel)
	channelKey := strings.ToLower(channel)

	merged := r.lookupBadgeSets(ctx, channelKey)
	if len(merged) == 0 {
		return badges
	}

	enriched := make([]core.ChatBadge, len(badges))
	copy(enriched, badges)

	enrichedCount := 0

	for i, badge := range enriched {
		if !strings.EqualFold(badge.Platform, "twitch") {
			continue
		}
		versions, ok := merged[badge.ID]
		if !ok {
			continue
		}
		if images := versions[badge.Version]; len(images) > 0 {
			enriched[i].Images = images
			enrichedCount++
			continue
		}
		if images := versions[""]; len(images) > 0 {
			enriched[i].Images = images
			enrichedCount++
		}
	}

	if enrichedCount > 0 {
		if _, seen := r.enriched.LoadOrStore(channel, struct{}{}); !seen {
			log.Printf("twitchbadges: enriched %d badges for channel=%s", enrichedCount, channelKey)
		}
	}

	return enriched
}

func (r *Resolver) lookupBadgeSets(ctx context.Context, channel string) map[string]map[string][]core.ChatBadgeImage {
	clientID := strings.TrimSpace(r.ClientID)
	clientSecret := strings.TrimSpace(r.ClientSecret)
	if clientID == "" || clientSecret == "" {
		return nil
	}

	token, err := r.appToken(ctx)
	if err != nil {
		log.Printf("twitchbadges: app token: %v", err)
		return nil
	}

	ttl := r.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}

	result := map[string]map[string][]core.ChatBadgeImage{}
	if globalSets, ok := r.cachedBadgeSets("global", ttl); ok {
		mergeBadgeSets(result, globalSets)
	} else if globalSets, err := r.fetchBadgeSets(ctx, token, ""); err == nil {
		r.storeBadgeSets("global", globalSets, ttl)
		log.Printf("twitchbadges: fetched global badge metadata (%d sets)", len(globalSets))
		mergeBadgeSets(result, globalSets)
	} else {
		log.Printf("twitchbadges: fetch global badges: %v", err)
	}

	if channel == "" {
		return result
	}

	broadcasterID := ""
	if isNumericID(channel) {
		broadcasterID = channel
	} else if cachedID, ok := r.cachedUserID(channel, ttl); ok {
		broadcasterID = cachedID
	} else if fetched, err := r.lookupUserID(ctx, token, channel); err == nil && fetched != "" {
		broadcasterID = fetched
		r.storeUserID(channel, fetched, ttl)
	} else if err != nil {
		log.Printf("twitchbadges: lookup user %s: %v", channel, err)
	}

	if broadcasterID == "" {
		return result
	}

	if channelSets, ok := r.cachedBadgeSets(broadcasterID, ttl); ok {
		mergeBadgeSets(result, channelSets)
		return result
	}

	if channelSets, err := r.fetchBadgeSets(ctx, token, broadcasterID); err == nil {
		r.storeBadgeSets(broadcasterID, channelSets, ttl)
		log.Printf("twitchbadges: fetched badge metadata for %s (%d sets)", channel, len(channelSets))
		mergeBadgeSets(result, channelSets)
	} else {
		log.Printf("twitchbadges: fetch badges for %s: %v", channel, err)
	}

	return result
}

func (r *Resolver) cachedBadgeSets(key string, ttl time.Duration) (map[string]map[string][]core.ChatBadgeImage, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.badgeSets == nil {
		return nil, false
	}
	entry, ok := r.badgeSets[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	sets, _ := entry.value.(map[string]map[string][]core.ChatBadgeImage)
	return sets, sets != nil
}

func (r *Resolver) storeBadgeSets(key string, sets map[string]map[string][]core.ChatBadgeImage, ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.badgeSets == nil {
		r.badgeSets = map[string]cacheEntry{}
	}
	r.badgeSets[key] = cacheEntry{value: sets, expiresAt: time.Now().Add(ttl)}
}

func (r *Resolver) cachedUserID(channel string, ttl time.Duration) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.users == nil {
		return "", false
	}
	entry, ok := r.users[channel]
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	id, _ := entry.value.(string)
	return id, id != ""
}

func (r *Resolver) storeUserID(channel, id string, ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.users == nil {
		r.users = map[string]cacheEntry{}
	}
	r.users[channel] = cacheEntry{value: id, expiresAt: time.Now().Add(ttl)}
}

func (r *Resolver) fetchBadgeSets(ctx context.Context, token, broadcasterID string) (map[string]map[string][]core.ChatBadgeImage, error) {
	base := strings.TrimSuffix(helixBaseURL, "/")
	endpoint := base + badgeGlobalPath
	if broadcasterID != "" {
		endpoint = base + badgeChannelPath + "?broadcaster_id=" + url.QueryEscape(broadcasterID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", strings.TrimSpace(r.ClientID))

	client := r.httpClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed helixBadgeResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return convertBadgeSets(parsed.Data), nil
}

func (r *Resolver) lookupUserID(ctx context.Context, token, channel string) (string, error) {
	base := strings.TrimSuffix(helixBaseURL, "/")
	endpoint := base + usersPath + "?login=" + url.QueryEscape(channel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", strings.TrimSpace(r.ClientID))

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	var parsed helixUsersResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(parsed.Data) == 0 || parsed.Data[0].ID == "" {
		return "", errors.New("user not found")
	}
	return parsed.Data[0].ID, nil
}

func (r *Resolver) appToken(ctx context.Context) (string, error) {
	r.mu.Lock()
	if r.token.token != "" && time.Now().Before(r.token.expiresAt) {
		token := r.token.token
		r.mu.Unlock()
		return token, nil
	}
	r.mu.Unlock()

	form := url.Values{}
	form.Set("client_id", strings.TrimSpace(r.ClientID))
	form.Set("client_secret", strings.TrimSpace(r.ClientSecret))
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, oauthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("request token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token status %d", resp.StatusCode)
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}

	token := strings.TrimSpace(parsed.AccessToken)
	if token == "" {
		return "", errors.New("empty access_token")
	}

	expiresIn := time.Duration(parsed.ExpiresIn) * time.Second
	if parsed.ExpiresIn <= 0 {
		expiresIn = time.Hour
	}

	r.mu.Lock()
	r.token = cachedToken{token: token, expiresAt: time.Now().Add(expiresIn)}
	r.mu.Unlock()

	return token, nil
}

func (r *Resolver) httpClient() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return http.DefaultClient
}

func mergeBadgeSets(dst map[string]map[string][]core.ChatBadgeImage, src map[string]map[string][]core.ChatBadgeImage) {
	for setID, versions := range src {
		if dst[setID] == nil {
			dst[setID] = map[string][]core.ChatBadgeImage{}
		}
		for version, images := range versions {
			dst[setID][version] = images
		}
	}
}

func convertBadgeSets(sets []helixBadgeSet) map[string]map[string][]core.ChatBadgeImage {
	result := make(map[string]map[string][]core.ChatBadgeImage, len(sets))
	for _, set := range sets {
		if set.SetID == "" {
			continue
		}
		versions := map[string][]core.ChatBadgeImage{}
		for _, v := range set.Versions {
			if v.ID == "" {
				continue
			}
			versions[v.ID] = buildImages(v)
		}
		if len(versions) > 0 {
			result[set.SetID] = versions
		}
	}
	return result
}

func buildImages(item helixBadgeItem) []core.ChatBadgeImage {
	var images []core.ChatBadgeImage
	add := func(url string, size int, suffix string) {
		if strings.TrimSpace(url) == "" {
			return
		}
		images = append(images, core.ChatBadgeImage{
			ID:     fmt.Sprintf("%s-%s", item.ID, suffix),
			URL:    url,
			Width:  size,
			Height: size,
		})
	}
	add(item.ImageURL1x, 18, "1x")
	add(item.ImageURL2x, 36, "2x")
	add(item.ImageURL4x, 72, "4x")
	return images
}

func isNumericID(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}
