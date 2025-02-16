package mocks

import (
	"context"
	"fmt"
	"sync"

	"github.com/MainfluxLabs/mainflux/webhooks"
)

var _ webhooks.WebhookRepository = (*webhookRepositoryMock)(nil)

type webhookRepositoryMock struct {
	mu       sync.Mutex
	counter  uint64
	webhooks map[string]webhooks.Webhook
}

func NewWebhookRepository() webhooks.WebhookRepository {
	return &webhookRepositoryMock{
		webhooks: make(map[string]webhooks.Webhook),
	}
}

func (wrm *webhookRepositoryMock) Save(_ context.Context, whs ...webhooks.Webhook) ([]webhooks.Webhook, error) {
	wrm.mu.Lock()
	defer wrm.mu.Unlock()

	for i := range whs {
		wrm.webhooks[key(whs[i].ThingID, whs[i].Url)] = whs[i]
	}
	return whs, nil
}

func (wrm *webhookRepositoryMock) RetrieveByThingID(_ context.Context, thingID string) ([]webhooks.Webhook, error) {
	wrm.mu.Lock()
	defer wrm.mu.Unlock()

	var whs []webhooks.Webhook
	for _, i := range wrm.webhooks {
		if i.ThingID == thingID {
			whs = append(whs, i)
		}
	}

	return whs, nil
}

func key(thingId string, url string) string {
	return fmt.Sprintf("%s-%s", thingId, url)
}
