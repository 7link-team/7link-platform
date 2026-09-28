package files

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"image"
	"image/png"
	"testing"
)

func TestMetadataBelongsToBytesNotNames(t *testing.T) {
	r := NewMetadataRegistry()
	require.Error(t, r.Register(nil))
	require.NoError(t, r.Register(ImageMetadata{}))
	require.Error(t, r.Register(ImageMetadata{}))
	var buffer bytes.Buffer
	require.NoError(t, png.Encode(&buffer, image.NewNRGBA(image.Rect(0, 0, 27, 41))))
	meta := r.Detect(buffer.Bytes())
	require.Equal(t, "image/png", meta.MIME)
	require.Equal(t, "image", meta.Type)
	require.Equal(t, "png", meta.Subtype)
	require.Equal(t, 27, *meta.Width)
	require.Equal(t, 41, *meta.Height)
	for _, name := range []string{"1.mp4", "2.mp3", "4.apk"} {
		require.NotEqual(t, "png", Extension(name))
		require.Equal(t, meta, r.Detect(buffer.Bytes()))
	}
	require.Equal(t, "unavailable", r.Detect(buffer.Bytes()[:16]).Status)
	unknown := r.Detect(bytes.Repeat([]byte{0}, MetadataPrefixLimit+1))
	require.Equal(t, "unknown", unknown.Status)
	require.Nil(t, unknown.Width)
}
