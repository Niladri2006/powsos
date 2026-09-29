package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port              string
	Host              string
	DBDriver          string
	DBSource          string
	JWTSecret         string
	StorageDir        string
	MaxUploadSizeMB   int64
	CORSAllowedOrigin string
	Environment       string
}

func Load() *Config {
	port := getEnv("PORT", "8080")
	host := getEnv("HOST", "0.0.0.0")
	dbDriver := getEnv("DB_DRIVER", "sqlite")
	dbSource := getEnv("DB_SOURCE", "pawsos.db")

	jwtSecret := getEnv("JWT_SECRET", "pawsos_production_default_jwt_secret_change_me_in_prod")
	storageDir := getEnv("STORAGE_DIR", "uploads")
	env := getEnv("ENVIRONMENT", "development")

	maxSizeStr := getEnv("MAX_UPLOAD_SIZE_MB", "5")
	maxSize, err := strconv.ParseInt(maxSizeStr, 10, 64)
	if err != nil || maxSize <= 0 {
		maxSize = 5
	}

	corsOrigin := getEnv("CORS_ALLOWED_ORIGIN", "*")

	return &Config{
		Port:              port,
		Host:              host,
		DBDriver:          dbDriver,
		DBSource:          dbSource,
		JWTSecret:         jwtSecret,
		StorageDir:        storageDir,
		MaxUploadSizeMB:   maxSize,
		CORSAllowedOrigin: corsOrigin,
		Environment:       env,
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
