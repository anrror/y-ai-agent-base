// Package builtin provides ready-to-use skill implementations.
package builtin

import (
	"github.com/anrror/y-ai-agent-base/pkg/skills"
	"github.com/anrror/y-ai-agent-base/pkg/tool"
)

// TimeSkill returns a skill that provides current time information.
// It wraps the tool.TimeTool and matches queries about time and dates.
func TimeSkill() skills.Skill {
	return skills.NewSkill(
		"time",
		skills.WithDescription("Provides current date and time information"),
		skills.WithInstructions(
			"Use the get_current_time tool to retrieve the current date and time. "+
				"Always format time responses in RFC 3339 format.",
		),
		skills.WithTools(tool.TimeTool()),
		skills.WithTags("time", "date", "clock", "now", "today"),
		skills.WithCategory("utility"),
		skills.WithVersion("1.0.0"),
		skills.WithAuthor("y-ai-agent-base"),
		skills.WithEstimatedTokens(50),
	)
}

// EchoSkill returns a skill that echoes back user input.
// It wraps the tool.EchoTool and matches queries about repetition and echoing.
func EchoSkill() skills.Skill {
	return skills.NewSkill(
		"echo",
		skills.WithDescription("Echoes back the provided input"),
		skills.WithInstructions(
			"Use the echo tool to repeat or mirror back the user's message. "+
				"Pass the message to be echoed as the 'message' parameter.",
		),
		skills.WithTools(tool.EchoTool()),
		skills.WithTags("echo", "repeat", "mirror", "say", "parrot"),
		skills.WithCategory("utility"),
		skills.WithVersion("1.0.0"),
		skills.WithAuthor("y-ai-agent-base"),
		skills.WithEstimatedTokens(30),
	)
}

// WeatherSkill returns a skill that provides mock weather information.
// It wraps the tool.WeatherTool and matches queries about weather.
func WeatherSkill() skills.Skill {
	return skills.NewSkill(
		"weather",
		skills.WithDescription("Provides mock weather information for any city"),
		skills.WithInstructions(
			"Use the get_weather tool to retrieve weather information for a city. "+
				"Provide the city name and optionally a temperature unit (celsius or fahrenheit). "+
				"Always summarize the weather conditions clearly for the user.",
		),
		skills.WithTools(tool.WeatherTool()),
		skills.WithTags("weather", "temperature", "forecast", "climate", "rain", "sunny"),
		skills.WithCategory("utility"),
		skills.WithVersion("1.0.0"),
		skills.WithAuthor("y-ai-agent-base"),
		skills.WithEstimatedTokens(80),
	)
}
