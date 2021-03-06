package server

import (
	"crypto/aes"
	"fmt"
	"github.com/thoas/picfit/crypt/aes256cbc"
	"github.com/thoas/picfit/image"
	"github.com/thoas/picfit/logger"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"

	"github.com/mholt/binding"

	api "gopkg.in/fukata/golang-stats-api-handler.v1"

	"github.com/thoas/picfit"
	"github.com/thoas/picfit/constants"
	"github.com/thoas/picfit/failure"
	"github.com/thoas/picfit/payload"
)

type handlers struct {
	processor *picfit.Processor
}

func (h handlers) stats(c *gin.Context) {
	c.JSON(http.StatusOK, api.GetStats())
}

func (h handlers) internalError(c *gin.Context) {
	panic(errors.WithStack(fmt.Errorf("KO")))
}

// healthcheck displays an ok response for healthcheck
func (h handlers) healthcheck(startedAt time.Time) func(c *gin.Context) {
	return func(c *gin.Context) {
		now := time.Now().UTC()

		uptime := now.Sub(startedAt)

		c.JSON(http.StatusOK, gin.H{
			"started_at": startedAt.String(),
			"uptime":     uptime.String(),
			"status":     "Ok",
			"version":    constants.Version,
			"revision":   constants.Revision,
			"build_time": constants.BuildTime,
			"compiler":   constants.Compiler,
			"ip_address": c.ClientIP(),
		})
	}
}

// display displays and image using resizing parameters
func (h handlers) display(c *gin.Context) error {
	file, err := h.processor.ProcessContext(c,
		picfit.WithAsync(true),
		picfit.WithLoad(true))
	if err != nil {
		return err
	}

	for k, v := range file.Headers {
		c.Header(k, v)
	}

	c.Header("Cache-Control", "must-revalidate")

	c.Data(http.StatusOK, file.ContentType(), file.Content())

	return nil
}

func (h handlers) secureDisplay(c *gin.Context) error {

	err := h.securePath(c)
	if err != nil {
		return err
	}

	return h.display(c)
}

// upload uploads an image to the destination storage
func (h handlers) upload(c *gin.Context) error {
	multipartPayload := new(payload.Multipart)
	errs := binding.Bind(c.Request, multipartPayload)
	if errs != nil {
		return errs
	}

	file, err := h.processor.Upload(multipartPayload)
	if err != nil {
		return err
	}

	c.JSON(http.StatusOK, gin.H{
		"filename": file.Filepath,
		"path":     file.Path(),
		"url":      file.URL(),
	})

	return nil
}

// delete deletes a file from storages
func (h handlers) delete(c *gin.Context) error {
	var (
		err         error
		path        = c.Param("parameters")
		key, exists = c.Get("key")
	)

	if path == "" && !exists {
		return failure.ErrUnprocessable
	}

	if !exists {
		err = h.processor.Delete(path[1:])
	} else {
		err = h.processor.DeleteChild(key.(string))
	}

	if err != nil {
		return err
	}

	c.String(http.StatusOK, "Ok")

	return nil
}

// get generates an image synchronously and return its information from storages
func (h handlers) get(c *gin.Context) error {
	file, err := h.processor.ProcessContext(c,
		picfit.WithAsync(false),
		picfit.WithLoad(false))
	if err != nil {
		return err
	}

	imageSizes, err := h.processor.GetSizes(file)
	if err != nil {
		return err
	}

	c.JSON(http.StatusOK, gin.H{
		"filename": file.Filename(),
		"path":     file.Path(),
		"url":      file.URL(),
		"key":      file.Key,
		"width":    imageSizes.Width,
		"height":   imageSizes.Height,
		"bytes":    imageSizes.Bytes,
	})

	return nil
}

func (h handlers) secureGet(c *gin.Context) error {

	err := h.securePath(c)
	if err != nil {
		return err
	}

	return h.get(c)
}

// redirect redirects to the image using base url from storage
func (h handlers) redirect(c *gin.Context) error {
	file, err := h.processor.ProcessContext(c,
		picfit.WithAsync(false),
		picfit.WithLoad(false))
	if err != nil {
		return err
	}

	c.Redirect(http.StatusMovedPermanently, file.URL())

	return nil
}

func (h handlers) secureRedirect(c *gin.Context) error {

	err := h.securePath(c)
	if err != nil {
		return err
	}

	return h.redirect(c)
}

func pprofHandler(h http.HandlerFunc) gin.HandlerFunc {
	handler := http.HandlerFunc(h)
	return func(c *gin.Context) {
		handler.ServeHTTP(c.Writer, c.Request)
	}
}

func (h handlers) info(c *gin.Context) error {

	path := c.Query("path")

	if path == "" {
		c.String(http.StatusBadRequest, "Request should contains path string")
		return nil
	}

	storage := h.processor.GetStorageByFileExist(path)
	if storage == nil {
		return failure.ErrFileNotExists
	}

	img := &image.ImageFile{
		Filepath: path,
		Storage:  storage,
	}

	imgSizes, err := h.processor.GetSizes(img)
	if err != nil {
		return err
	}

	c.JSON(http.StatusOK, gin.H{
		"filename": img.Filename(),
		"path":     img.Path(),
		"url":      img.URL(),
		"width":    imgSizes.Width,
		"height":   imgSizes.Height,
		"bytes":    imgSizes.Bytes,
	})

	return nil
}

func (h handlers) exist(c *gin.Context) error {

	path := c.Query("path")

	if path == "" {
		c.String(http.StatusBadRequest, "Request should contains path string")
		return nil
	}

	storage := h.processor.GetStorageByFileExist(path)
	if storage == nil {
		return failure.ErrFileNotExists
	}

	return nil
}

func (h handlers) securePath(c *gin.Context) error {

	parameters := c.MustGet("parameters").(map[string]interface{})
	encodedPath := parameters["path"].(string)

	path, err := aes256cbc.Decode(
		encodedPath,
		h.processor.SecurePathKey[:aes.BlockSize],
		h.processor.SecurePathKey[aes.BlockSize:],
	)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return err
	}

	h.processor.Logger.Info("Path decoded", logger.String("path", path))

	h.processor.SetSecuredOptions(c, path)

	return nil
}
