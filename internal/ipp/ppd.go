package ipp

import (
	"context"

	"github.com/OpenPrinting/goipp"
)

// PPD is a printer driver CUPS can offer for a queue.
type PPD struct {
	// Name is the identifier passed back when creating a printer, for example
	// "drv:///sample.drv/generic.ppd" or "everywhere".
	Name string

	Make            string
	MakeAndModel    string
	Product         string
	NaturalLanguage string

	// DeviceID is the IEEE 1284 string this driver claims to support. It is what
	// makes automatic driver selection possible: a device reports its own ID, and
	// CUPS can narrow the catalogue to drivers advertising a match.
	DeviceID string

	// Type is the ppd-type: postscript, pdf, raster, fax, object, unknown.
	Type string
}

// PPDFilter narrows a driver search. Every field is optional, and the zero value
// asks for the entire catalogue.
type PPDFilter struct {
	// DeviceID is the IEEE 1284 string reported by the hardware. This is the
	// field that matters: it is how one-click pairing finds the right driver
	// without asking the user to recognise their own printer in a list of
	// thousands.
	DeviceID string

	Make         string
	MakeAndModel string
	Product      string
	Type         string

	// Limit caps how many drivers come back. Zero means no limit, which on a
	// full installation means every driver on the machine. See PPDs.
	Limit int
}

var ppdFields = []string{
	"ppd-name",
	"ppd-make",
	"ppd-make-and-model",
	"ppd-device-id",
	"ppd-product",
	"ppd-natural-language",
	"ppd-type",
}

// PPDs lists drivers CUPS can offer, narrowed by filter.
//
// **Filter whenever you can.** printer-cycle installs every open driver
// available, which on a normal box is close to eighteen thousand PPDs. Asking
// for all of them means CUPS builds, sends, and this process decodes a response
// measured in megabytes, which is a poor idea on a Raspberry Pi Zero 2 W with
// 512MB of RAM. Filtering by DeviceID returns a handful.
func (c *Client) PPDs(ctx context.Context, filter PPDFilter) ([]PPD, error) {
	req := c.NewRequest(goipp.OpCupsGetPpds)
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI, goipp.String(c.RootURI())))

	if filter.DeviceID != "" {
		req.Operation.Add(goipp.MakeAttribute("ppd-device-id", goipp.TagText, goipp.String(filter.DeviceID)))
	}
	if filter.Make != "" {
		req.Operation.Add(goipp.MakeAttribute("ppd-make", goipp.TagText, goipp.String(filter.Make)))
	}
	if filter.MakeAndModel != "" {
		req.Operation.Add(goipp.MakeAttribute("ppd-make-and-model", goipp.TagText, goipp.String(filter.MakeAndModel)))
	}
	if filter.Product != "" {
		req.Operation.Add(goipp.MakeAttribute("ppd-product", goipp.TagText, goipp.String(filter.Product)))
	}
	if filter.Type != "" {
		req.Operation.Add(goipp.MakeAttribute("ppd-type", goipp.TagKeyword, goipp.String(filter.Type)))
	}
	if filter.Limit > 0 {
		req.Operation.Add(goipp.MakeAttribute("limit", goipp.TagInteger, goipp.Integer(filter.Limit)))
	}
	req.Operation.Add(requestedAttributes(ppdFields...))

	resp, err := c.Do(ctx, "/", req, nil)
	if err != nil {
		return nil, err
	}
	if err := check(goipp.OpCupsGetPpds, resp); err != nil {
		// A filter matching nothing is an empty result, not a failure. Callers
		// need to distinguish "no driver claims this printer" from "the query
		// broke", and only the first is expected often enough to be normal.
		if errorsIsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	var ppds []PPD
	for _, g := range resp.Groups {
		if g.Tag != goipp.TagPrinterGroup {
			continue
		}
		name := str(g.Attrs, "ppd-name")
		if name == "" {
			continue
		}
		ppds = append(ppds, PPD{
			Name:            name,
			Make:            str(g.Attrs, "ppd-make"),
			MakeAndModel:    str(g.Attrs, "ppd-make-and-model"),
			Product:         str(g.Attrs, "ppd-product"),
			NaturalLanguage: str(g.Attrs, "ppd-natural-language"),
			DeviceID:        str(g.Attrs, "ppd-device-id"),
			Type:            str(g.Attrs, "ppd-type"),
		})
	}
	return ppds, nil
}
