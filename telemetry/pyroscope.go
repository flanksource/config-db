package telemetry

import (
	"os"

	"github.com/flanksource/commons/logger"
	"github.com/grafana/pyroscope-go"
)

func StartPyroscope(serviceName, address string) {
	pyroscope.Start(pyroscope.Config{
		ApplicationName: serviceName,

		// address of pyroscope server <http://pyroscope-server:4040>
		ServerAddress: address,

		BasicAuthUser:     os.Getenv("PYROSCOPE_USER"),
		BasicAuthPassword: os.Getenv("PYROSCOPE_PASSWORD"),

		// disable logging by setting this to nil
		Logger: logger.GetLogger("pyroscope"),

		Tags: map[string]string{
			"hostname": os.Getenv("HOSTNAME"),
			"env":      os.Getenv("PYROSCOPE_ENV"),
		},

		ProfileTypes: []pyroscope.ProfileType{
			// these profile types are enabled by default:
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,

			// these profile types are optional:
			//pyroscope.ProfileGoroutines,
			//pyroscope.ProfileMutexCount,
			//pyroscope.ProfileMutexDuration,
			//pyroscope.ProfileBlockCount,
			//pyroscope.ProfileBlockDuration,
		},
	})
}
