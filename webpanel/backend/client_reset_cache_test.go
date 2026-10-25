package main

import "testing"

// A GET /clients that began before a reset must not restore its old values
// after the reset invalidates the server cache.
func TestClientCacheGenerationAfterTrafficReset(t *testing.T) {
	invalidateClientsCache()
	clientsCache.mutex.RLock()
	initial := clientsCache.generation
	clientsCache.mutex.RUnlock()

	oldClients := []interface{}{map[string]interface{}{"name": "fati", "traffic_used_bytes": int64(3000000000)}}
	if !cacheClientsIfCurrent(initial, oldClients) {
		t.Fatal("initial client cache population unexpectedly rejected")
	}

	// Simulate PostgreSQL traffic reset finishing while an old GET is in flight.
	invalidateClientsCache()
	if cacheClientsIfCurrent(initial, oldClients) {
		t.Fatal("pre-reset GET repopulated the cache after invalidation")
	}

	clientsCache.mutex.RLock()
	if clientsCache.data != nil {
		clientsCache.mutex.RUnlock()
		t.Fatal("cache still contains stale clients after reset")
	}
	updatedGeneration := clientsCache.generation
	clientsCache.mutex.RUnlock()

	zeroClients := []interface{}{map[string]interface{}{
		"name": "fati", "traffic_used_bytes": int64(0),
		"traffic_used_bytes_authoritative": true,
	}}
	if !cacheClientsIfCurrent(updatedGeneration, zeroClients) {
		t.Fatal("post-reset cache population rejected")
	}

	clientsCache.mutex.RLock()
	stored, ok := clientsCache.data[0].(map[string]interface{})
	clientsCache.mutex.RUnlock()
	if !ok || stored["traffic_used_bytes"] != int64(0) {
		t.Fatalf("post-reset authoritative zero was not cached: %v", stored)
	}

	// Do not leave global cache content behind for other tests.
	invalidateClientsCache()
}
