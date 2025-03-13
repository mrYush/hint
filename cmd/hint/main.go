package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/context"
	"github.com/mrYush/hint/internal/llm"
	"github.com/spf13/viper"
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "hint [вопрос]",
		Short: "Утилита для получения контекстных подсказок с использованием LLM",
		Args:  cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			// Загрузка конфигурации
			cfg, err := config.Load()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Ошибка загрузки конфигурации: %v\n", err)
				os.Exit(1)
			}

			// Получение контекста директории
			ctx, err := context.GetDirectoryContext()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Ошибка получения контекста: %v\n", err)
				os.Exit(1)
			}

			// Формирование вопроса из аргументов
			question := args[0]
			for i := 1; i < len(args); i++ {
				question += " " + args[i]
			}

			// Запрос к LLM
			response, err := llm.AskLLM(cfg, ctx, question)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Ошибка при запросе к LLM: %v\n", err)
				os.Exit(1)
			}

			// Вывод ответа
			fmt.Println(response)
		},
	}

	// Флаги для конфигурации
	rootCmd.PersistentFlags().String("api-url", "", "URL API (по умолчанию используется OpenAI)")
	rootCmd.PersistentFlags().String("api-key", "", "Ключ API")
	rootCmd.PersistentFlags().String("model", "gpt-4", "Название модели")
	
	// Связывание флагов с Viper
	viper.BindPFlag("api_url", rootCmd.PersistentFlags().Lookup("api-url"))
	viper.BindPFlag("api_key", rootCmd.PersistentFlags().Lookup("api-key"))
	viper.BindPFlag("model", rootCmd.PersistentFlags().Lookup("model"))
	
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
} 