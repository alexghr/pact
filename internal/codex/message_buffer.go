package codex

const messageBufferSize = 256

type messageBuffer struct {
	items [messageBufferSize]Message
	head  int
	size  int
}

func (b *messageBuffer) push(message Message) bool {
	// reject messages if the ring buffer is full
	if b.size == len(b.items) {
		return false
	}
	index := (b.head + b.size) % len(b.items)
	b.items[index] = message
	b.size++
	return true
}

func (b *messageBuffer) pop() (Message, bool) {
	if b.size == 0 {
		return Message{}, false
	}
	message := b.items[b.head]
	b.items[b.head] = Message{}
	b.head = (b.head + 1) % len(b.items)
	b.size--
	return message, true
}
