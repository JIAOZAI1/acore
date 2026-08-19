// Command main 是当前事件总线的用法示例。
package main

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/event"
)

type UserEvent struct {
	User  string
	Email string
}

// Name implements [event.Event].
func (u UserEvent) Name() string {
	return "user event"
}

func main() {
	bus := event.NewBus()
	first, err := event.Subscribe(bus, func(ctx context.Context, u UserEvent) error {
		fmt.Printf("event:%s,email:%s\n", u.Name(), u.Email)
		return nil
	})
	if err != nil {
		fmt.Printf("subscribe first handler: %v\n", err)
		return
	}
	defer first.Unsubscribe()

	second, err := event.Subscribe(bus, func(ctx context.Context, u UserEvent) error {
		fmt.Printf("event:%s,username:%s\n", u.Name(), u.User)
		return nil
	})
	if err != nil {
		fmt.Printf("subscribe second handler: %v\n", err)
		return
	}
	defer second.Unsubscribe()

	if err := bus.Publish(context.Background(), UserEvent{User: "lam", Email: "lin.jing.peng@qq.com"}); err != nil {
		fmt.Printf("publish user event: %v\n", err)
	}
}
