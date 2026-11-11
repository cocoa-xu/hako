//go:build cgo

package collector

/*
#include <mach/mach.h>

static kern_return_t hako_cpu_ticks(host_cpu_load_info_data_t *info) {
    mach_msg_type_number_t count = HOST_CPU_LOAD_INFO_COUNT;
    mach_port_t host = mach_host_self();
    kern_return_t result = host_statistics(host, HOST_CPU_LOAD_INFO,
        (host_info_t)info, &count);
    mach_port_deallocate(mach_task_self(), host);
    return result;
}
*/
import "C"

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/cocoa-xu/hako/internal/report"
)

func (c *Collector) cpu(_ context.Context) (report.CPU, error) {
	var info C.host_cpu_load_info_data_t
	if status := C.hako_cpu_ticks(&info); status != C.KERN_SUCCESS {
		c.cpuPrevious = nil
		return report.CPU{}, fmt.Errorf("host_statistics failed: %d", status)
	}
	current := cpuObservation{at: time.Now()}
	for i := range current.ticks {
		current.ticks[i] = uint32(info.cpu_ticks[i])
	}
	previous := c.cpuPrevious
	c.cpuPrevious = &current
	return cpuBetween(previous, current, runtime.NumCPU())
}
