package impl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"digital-twin/internal/tools"
)

// TimeSchema 当前时间工具：无参数。
const TimeSchema = `{"type": "object", "properties": {}}`

func NewCurrentTime() tools.Tool {
	return tools.Tool{
		Name:        "current_time",
		Description: "获取当前日期和时间（北京时间）。适合需要知道今天/现在几点、计算日期差、安排日程的场景。",
		Parameters:  json.RawMessage(TimeSchema),
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
			now := time.Now()
			week := map[time.Weekday]string{
				time.Sunday: "周日", time.Monday: "周一", time.Tuesday: "周二",
				time.Wednesday: "周三", time.Thursday: "周四", time.Friday: "周五", time.Saturday: "周六",
			}[now.Weekday()]
			return fmt.Sprintf("当前时间：%s %s（北京时间）", now.Format("2006-01-02 15:04:05"), week), nil
		},
	}
}
