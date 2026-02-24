package utils

// MapKeys returns a slice of all keys from the given map
func MapKeys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// MapValues returns a slice of all values from the given map
func MapValues[K comparable, V any](m map[K]V) []V {
	values := make([]V, 0, len(m))
	for _, v := range m {
		values = append(values, v)
	}
	return values
}

// KeyValue represents a key-value pair
type KeyValue[K comparable, V any] struct {
	Key   K
	Value V
}

// MapToSlice returns a slice of KeyValue pairs from the given map
func MapToSlice[K comparable, V any](m map[K]V) []KeyValue[K, V] {
	items := make([]KeyValue[K, V], 0, len(m))
	for k, v := range m {
		items = append(items, KeyValue[K, V]{Key: k, Value: v})
	}
	return items
}
