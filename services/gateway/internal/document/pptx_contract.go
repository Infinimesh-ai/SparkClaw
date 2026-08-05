package document

import "fmt"

const (
	PPTXWholeDeckMaxSlides  = 12
	PPTXMaxUpdatedShapes    = 64
	PPTXMaxReplacementBytes = 32 << 10
)

func ValidatePPTXEditBounds(slides, shapes, replacementBytes int) error {
	if slides > PPTXWholeDeckMaxSlides {
		return fmt.Errorf("PPTX edit exceeds the %d-slide update bound", PPTXWholeDeckMaxSlides)
	}
	if shapes > PPTXMaxUpdatedShapes {
		return fmt.Errorf("PPTX edit exceeds the %d-shape update bound", PPTXMaxUpdatedShapes)
	}
	if replacementBytes > PPTXMaxReplacementBytes {
		return fmt.Errorf("PPTX edit exceeds the %d-byte replacement bound", PPTXMaxReplacementBytes)
	}
	return nil
}
