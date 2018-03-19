package main

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/vision/apiv1"
	"github.com/disintegration/imaging"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"google.golang.org/api/option"
	"google.golang.org/appengine"
	"google.golang.org/appengine/log"
	"google.golang.org/appengine/urlfetch"
)

type TemplateRenderer struct {
	templates *template.Template
}

func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

func init() {
	e := echo.New()
	e.Use(middleware.Recover())
	e.Use(middleware.Gzip())
	e.Renderer = &TemplateRenderer{
		templates: template.Must(template.ParseGlob("./templates/*.gohtml")),
	}
	e.Static("/assets", "./assets")

	e.GET("/", handleGetIndex)
	e.POST("/", handlePostIndex)

	http.Handle("/", e)
}

func handleGetIndex(c echo.Context) error {
	imageUrl := c.QueryParam("url")
	if imageUrl == "" {
		return c.Render(http.StatusOK, "index", nil)
	}

	ctx := appengine.NewContext(c.Request())

	ctxWithTimeout, _ := context.WithTimeout(ctx, 30*time.Second)
	imgResp, err := urlfetch.Client(ctxWithTimeout).Get(imageUrl)
	if err != nil {
		log.Errorf(ctx, "failed to get image: %s", err)
		return echo.NewHTTPError(http.StatusGatewayTimeout)
	}
	defer imgResp.Body.Close()

	img, format, err := image.Decode(imgResp.Body)
	if err != nil {
		log.Errorf(ctx, "failed to decode image: %s", err)
		return echo.NewHTTPError(http.StatusBadGateway)
	}

	gopherizedBytes, err := gopherize(ctx, img, format)
	if err != nil {
		return err
	}

	c.Response().Header().Set("Content-Type", "image/"+format)
	c.Response().Write(gopherizedBytes)
	return nil
}

func handlePostIndex(c echo.Context) error {
	ctx := appengine.NewContext(c.Request())

	fileHeader, err := c.FormFile("image")
	if err != nil {
		log.Errorf(ctx, "failed to get form file: %s", err)
		return echo.NewHTTPError(http.StatusBadRequest)
	}
	file, err := fileHeader.Open()
	if err != nil {
		log.Errorf(ctx, "failed to open form file: %s", err)
		return echo.NewHTTPError(http.StatusInternalServerError)
	}
	defer file.Close()

	img, format, err := image.Decode(file)
	if err != nil {
		log.Errorf(ctx, "failed to decode image: %s", err)
		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	gopherizedBytes, err := gopherize(ctx, img, format)
	if err != nil {
		return err
	}

	c.Response().Header().Set("Content-Type", "image/"+format)
	c.Response().Write(gopherizedBytes)
	return nil
}

func gopherize(ctx context.Context, img image.Image, format string) ([]byte, error) {
	baseImage, err := imgToBytes(img, format)
	if err != nil {
		log.Errorf(ctx, "failed to convert image to bytes: %s", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError)
	}

	client, err := vision.NewImageAnnotatorClient(ctx, option.WithCredentialsFile("./service_account.json"))
	if err != nil {
		log.Errorf(ctx, "failed to create client: %s", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError)
	}

	visionImage, err := vision.NewImageFromReader(bytes.NewBuffer(baseImage))
	if err != nil {
		log.Errorf(ctx, "failed to read image: %s", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError)
	}

	faces, err := client.DetectFaces(ctx, visionImage, nil, 0)
	if err != nil {
		log.Errorf(ctx, "failed to detect faces: %s", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError)
	}

	dstImage := imaging.New(img.Bounds().Dx(), img.Bounds().Dy(), image.Transparent)
	dstImage = imaging.Paste(dstImage, img, image.Pt(0, 0))

	eyeFile, err := os.Open("./gopher/eye.png")
	if err != nil {
		log.Errorf(ctx, "failed to open eye image: %s", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError)
	}
	defer eyeFile.Close()

	eyeImg, err := png.Decode(eyeFile)
	if err != nil {
		log.Errorf(ctx, "failed to decode eye image: %s", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError)
	}

	// Eyes
	for _, face := range faces {
		faceLandmarks := vision.FaceFromLandmarks(face.Landmarks)
		leftEye := faceLandmarks.Eyes.Left.Center
		if leftEye == nil {
			continue
		}
		rightEye := faceLandmarks.Eyes.Right.Center
		if rightEye == nil {
			continue
		}

		gopherEyeSize := int(math.Abs(float64(leftEye.X) - float64(rightEye.X)))
		gopherEye := imaging.Resize(eyeImg, gopherEyeSize, 0, imaging.Lanczos)

		leftEyeX := int(leftEye.X) - gopherEyeSize/2
		leftEyeY := int(leftEye.Y) - gopherEyeSize/2
		dstImage = imaging.Overlay(dstImage, gopherEye, image.Pt(leftEyeX, leftEyeY), 1.0)
		rightEyeX := int(rightEye.X) - gopherEyeSize/2
		rightEyeY := int(rightEye.Y) - gopherEyeSize/2
		dstImage = imaging.Overlay(dstImage, gopherEye, image.Pt(rightEyeX, rightEyeY), 1.0)
	}

	mouthFile, err := os.Open("./gopher/mouth.png")
	if err != nil {
		log.Errorf(ctx, "failed to open mouth image: %s", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError)
	}
	defer mouthFile.Close()

	mouthImg, err := png.Decode(mouthFile)
	if err != nil {
		log.Errorf(ctx, "failed to decode mouth image: %s", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError)
	}

	for _, face := range faces {
		faceLandmarks := vision.FaceFromLandmarks(face.Landmarks)
		nose := faceLandmarks.Nose.Tip
		if nose == nil {
			continue
		}
		mouthLeft := faceLandmarks.Mouth.Left
		if mouthLeft == nil {
			continue
		}
		mouthRight := faceLandmarks.Mouth.Right
		if mouthRight == nil {
			continue
		}

		gopherMouthSize := int(math.Abs(float64(mouthRight.X) - float64(mouthLeft.X)))
		gopherMouth := imaging.Resize(mouthImg, gopherMouthSize, 0, imaging.Lanczos)

		gopherMouthX := int(nose.X) - gopherMouth.Rect.Dx()/2
		dstImage = imaging.Overlay(dstImage, gopherMouth, image.Pt(gopherMouthX, int(nose.Y)), 1.0)
	}

	dstImageBytes, err := imgToBytes(dstImage, format)
	if err != nil {
		log.Errorf(ctx, "failed to convert dstimage to bytes: %s", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError)
	}

	return dstImageBytes, nil
}

func imgToBytes(img image.Image, format string) ([]byte, error) {
	buf := new(bytes.Buffer)
	switch format {
	case "png":
		if err := png.Encode(buf, img); err != nil {
			return nil, err
		}
	case "jpeg":
		if err := jpeg.Encode(buf, img, nil); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid format %s", format)
	}
	return buf.Bytes(), nil
}
