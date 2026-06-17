package repository

import (
	"blackwatch/internal/models"

	lru "github.com/hashicorp/golang-lru/v2"
)

type PlayerCache struct {
	cache *lru.Cache[string, int]
}

func NewPlayerCache(size int) (*PlayerCache, error) {
	cache, err := lru.New[string, int](size)
	if err != nil {
		return nil, err
	}
	return &PlayerCache{cache: cache}, nil
}

func (c *PlayerCache) Get(name string) (int, bool) {
	return c.cache.Get(name)
}

func (c *PlayerCache) Set(name string, id int) {
	c.cache.Add(name, id)
}

func (c *PlayerCache) Delete(name string) {
	c.cache.Remove(name)
}

func (c *PlayerCache) Clear() {
	c.cache.Purge()
}

func (c *PlayerCache) LoadAll(players []models.Player) {
	for _, p := range players {
		normalized := normalizeForComparison(p.Name)
		c.cache.Add(normalized, p.ID)
	}
}

func (c *PlayerCache) Size() int {
	return c.cache.Len()
}
