package changes

import (
	"strconv"

	"github.com/flanksource/gomplate/v3"
)

func evaluateCelExpression(expression string, env map[string]any) (bool, error) {
	res, err := gomplate.RunTemplate(env, gomplate.Template{Expression: expression})
	if err != nil {
		return false, err
	}

	return strconv.ParseBool(res)
}
