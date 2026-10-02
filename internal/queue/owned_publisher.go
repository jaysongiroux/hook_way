package queue

import "sync"

// ownedPublisher attaches native client cleanup without assuming ownership of
// clients injected into the public publisher constructors. Server shutdown must
// drain requests before closing publishers; CloseIdleConnections alone does
// not interrupt active SDK operations.
type ownedPublisher struct {
	Publisher
	closeIdle func()
	once      sync.Once
	err       error
}

func (p *ownedPublisher) Close() error {
	p.once.Do(func() {
		defer p.closeIdle()
		p.err = p.Publisher.Close()
	})
	return p.err
}
