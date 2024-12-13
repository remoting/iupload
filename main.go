package main

import (
	"iupload/fileserver"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

const STATIC_FOLDER = "static"

// 静态文件中间件
func staticFileMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 将请求路径映射到静态文件目录
		requestPath := c.Request.URL.Path
		fullPath := filepath.Join(STATIC_FOLDER, requestPath)
		// 检查文件是否存在并返回文件
		if _, err := filepath.Abs(fullPath); err == nil {
			http.ServeFile(c.Writer, c.Request, fullPath)
			c.Abort()
		} else {
			c.Next()
		}
	}
}
func main() {
	_serve := &fileserver.FileServer{
		Root:       STATIC_FOLDER,
		Browse:     &fileserver.Browse{},
		IndexNames: []string{"index.html"},
	}
	gin.SetMode(gin.DebugMode)
	// 创建一个默认的 Gin 路由器
	router := gin.Default()

	// 设置下载文件的路由
	router.GET("/_download", func(c *gin.Context) {
		id := c.Query("file")
		if strings.Contains(id, "..") {
			c.JSON(http.StatusBadRequest, gin.H{"message": "Invalid filename. Please check and try again."})
		} else {
			savePath := filepath.Join(".", STATIC_FOLDER)
			_, notExistErr := os.Stat(savePath)
			if os.IsNotExist(notExistErr) {
				_ = os.MkdirAll(savePath, os.ModePerm)
			}
			localPath := filepath.Join(savePath, id)
			fileInfo, fileExistErr := os.Stat(localPath)
			if os.IsNotExist(fileExistErr) {
				c.JSON(http.StatusBadRequest, gin.H{"message": "file not found"})
			} else {
				// 对文件名进行 URL 编码
				encodedFilename := url.QueryEscape(fileInfo.Name())
				c.Writer.Header().Set("Content-Disposition", "attachment; filename=\""+fileInfo.Name()+"\"; filename*=UTF-8''"+encodedFilename)
				c.Writer.Header().Set("Content-Type", "application/octet-stream")
				http.ServeFile(c.Writer, c.Request, localPath)
			}
		}
	})
	// 设置文件上传的路由
	router.POST("/_upload", func(c *gin.Context) {
		// 从请求中获取文件
		// 限制最大内存使用数量为 1MB ，不是限制客户端上传的文件大小
		err := c.Request.ParseMultipartForm(1 << 20)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": err.Error(),
			})
			return
		}
		// 遍历所有上传的文件字段
		for key, fileHeaders := range c.Request.MultipartForm.File {
			for _, file := range fileHeaders {
				// 打印文件名称
				log.Printf("Received %s=%s\n", key, file.Filename)
				savePath := filepath.Join(".", STATIC_FOLDER)
				_, notExistErr := os.Stat(savePath)
				if os.IsNotExist(notExistErr) {
					_ = os.MkdirAll(savePath, os.ModePerm)
				}
				// 保存文件到指定目录（当前目录）
				dst := filepath.Join(".", STATIC_FOLDER, file.Filename)
				if err := c.SaveUploadedFile(file, dst); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{
						"error": err.Error(),
					})
					return
				}
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"message": "uploaded successfully!",
		})
	})

	// 中间件来处理静态文件请求，排除 /download 路径
	router.NoRoute(func(c *gin.Context) {
		if c.Request.URL.Path == "/_upload" || c.Request.URL.Path == "/_download" {
			c.Next()
		} else {
			err := _serve.ServeHTTP(c.Writer, c.Request)
			if err != nil {
				c.JSON(200, gin.H{"msg": err.Error()})
			}
		}
	})

	// 启动服务器
	address := ":44321"
	log.Printf("Listening and serving HTTP on %s\n", address)
	err := router.Run(address)
	if err != nil {
		log.Printf("Failed to start: %s", err.Error())
	}
}
