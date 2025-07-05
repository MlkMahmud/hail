package utils

type Semaphore chan struct{}

func NewSemaphore(capacity int) Semaphore {
	return make(chan struct{}, capacity)
}

func (s Semaphore) Acquire() {
	s <- struct{}{}
}

func (s Semaphore) Release() {
	<-s
}
