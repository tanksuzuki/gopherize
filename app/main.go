package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"io/ioutil"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/disintegration/imaging"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"google.golang.org/appengine"
	"google.golang.org/appengine/log"
	"google.golang.org/appengine/urlfetch"
)

var (
	apiKey              string
	errLandmarkNotFound = errors.New("landmark not found")
)

type RequestJSON struct {
	Requests []Request `json:"requests"`
}
type Request struct {
	Image    RequestImage     `json:"image"`
	Features []RequestFeature `json:"features"`
}
type RequestImage struct {
	Content string `json:"content"` // Base64
}
type RequestFeature struct {
	Type string `json:"type"` // FACE_DETECTION
}

type ResponseJSON struct {
	Responses []Response `json:"responses"`
}
type Response struct {
	FaceAnnotations []ResponseFaceAnnotation `json:"faceAnnotations"`
}
type ResponseFaceAnnotation struct {
	Landmarks []ResponseLandmark `json:"landmarks"`
}
type ResponseLandmark struct {
	Type     string           `json:"type"`
	Position ResponsePosition `json:"position"`
}
type ResponsePosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

func (r ResponseFaceAnnotation) Get(name string) (ResponsePosition, error) {
	for _, landmark := range r.Landmarks {
		if landmark.Type == name {
			return landmark.Position, nil
		}
	}
	return ResponsePosition{}, fmt.Errorf("not found")
}

type TemplateRenderer struct {
	templates *template.Template
}

func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	return t.templates.ExecuteTemplate(w, name, data)
}

type Credential struct {
	Key string `json:"key"`
}

func init() {
	credentialsBytes, err := ioutil.ReadFile("./credentials.json")
	if err != nil {
		fmt.Println("failed to open app/credentials.json:", err)
		os.Exit(1)
	}
	var credential Credential
	if err := json.Unmarshal(credentialsBytes, &credential); err != nil {
		fmt.Println("failed to unmarshal app/credential.json:", err)
		os.Exit(1)
	}
	apiKey = credential.Key

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
	imgBytes, err := imgToBytes(img, format)
	if err != nil {
		log.Errorf(ctx, "failed to convert image to bytes: %s", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError)
	}
	imgBase64 := base64.StdEncoding.EncodeToString(imgBytes)

	request := RequestJSON{
		Requests: []Request{
			{
				Image: RequestImage{
					Content: imgBase64,
				},
				Features: []RequestFeature{
					{
						Type: "FACE_DETECTION",
					},
				},
			},
		},
	}
	requestJSON, err := json.Marshal(&request)
	if err != nil {
		log.Errorf(ctx, "failed to marshal request json: %s", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError)
	}

	apiResp, err := urlfetch.Client(ctx).Post(
		"https://vision.googleapis.com/v1/images:annotate?key="+apiKey,
		"application/json",
		bytes.NewBuffer(requestJSON),
	)
	if err != nil {
		log.Errorf(ctx, "failed to get response from cloud vision api: %s", err)
		return nil, echo.NewHTTPError(http.StatusGatewayTimeout)
	}
	defer apiResp.Body.Close()

	apiRespBytes, err := ioutil.ReadAll(apiResp.Body)
	if err != nil {
		log.Errorf(ctx, "failed to read response body from cloud vision api: %s", err)
		return nil, echo.NewHTTPError(http.StatusBadGateway)
	}

	var responseJSON ResponseJSON
	if err := json.Unmarshal(apiRespBytes, &responseJSON); err != nil {
		log.Errorf(ctx, "failed to unmarshal json: %s", err)
		return nil, echo.NewHTTPError(http.StatusInternalServerError)
	}

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

	dstImage := imaging.New(img.Bounds().Dx(), img.Bounds().Dy(), image.Transparent)
	dstImage = imaging.Paste(dstImage, img, image.Pt(0, 0))

	// Eyes
	for _, landmarks := range responseJSON.Responses[0].FaceAnnotations {
		leftEye, err := landmarks.Get("LEFT_EYE")
		if err != nil && err != errLandmarkNotFound {
			log.Errorf(ctx, "failed to get left eye info: %s", err)
			return nil, echo.NewHTTPError(http.StatusInternalServerError)
		}
		if err == errLandmarkNotFound {
			continue
		}

		rightEye, err := landmarks.Get("RIGHT_EYE")
		if err != nil && err != errLandmarkNotFound {
			log.Errorf(ctx, "failed to get right eye info: %s", err)
			return nil, echo.NewHTTPError(http.StatusInternalServerError)
		}
		if err == errLandmarkNotFound {
			continue
		}

		gopherEyeSize := int(math.Abs(leftEye.X - rightEye.X))
		gopherEye := imaging.Resize(eyeImg, gopherEyeSize, 0, imaging.Lanczos)

		leftEyeX := int(leftEye.X) - gopherEyeSize/2
		leftEyeY := int(leftEye.Y) - gopherEyeSize/2
		dstImage = imaging.Overlay(dstImage, gopherEye, image.Pt(leftEyeX, leftEyeY), 1.0)
		rightEyeX := int(rightEye.X) - gopherEyeSize/2
		rightEyeY := int(rightEye.Y) - gopherEyeSize/2
		dstImage = imaging.Overlay(dstImage, gopherEye, image.Pt(rightEyeX, rightEyeY), 1.0)
	}

	// Mouth
	for _, landmarks := range responseJSON.Responses[0].FaceAnnotations {
		nose, err := landmarks.Get("NOSE_TIP")
		if err != nil && err != errLandmarkNotFound {
			log.Errorf(ctx, "failed to get nose info: %s", err)
			return nil, echo.NewHTTPError(http.StatusInternalServerError)
		}
		if err == errLandmarkNotFound {
			continue
		}

		mouthLeft, err := landmarks.Get("MOUTH_LEFT")
		if err != nil && err != errLandmarkNotFound {
			log.Errorf(ctx, "failed to get mouth left info: %s", err)
			return nil, echo.NewHTTPError(http.StatusInternalServerError)
		}
		if err == errLandmarkNotFound {
			continue
		}

		mouthRight, err := landmarks.Get("MOUTH_RIGHT")
		if err != nil && err != errLandmarkNotFound {
			log.Errorf(ctx, "failed to get mouth right info: %s", err)
			return nil, echo.NewHTTPError(http.StatusInternalServerError)
		}
		if err == errLandmarkNotFound {
			continue
		}
		gopherMouthSize := int(math.Abs(mouthRight.X - mouthLeft.X))
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
