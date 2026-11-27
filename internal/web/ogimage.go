package web

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"

	"github.com/mdpsec/huntdeck/internal/humanize"
	"github.com/mdpsec/huntdeck/internal/store"
)

// A public program's share image (GET /programs/{platform}/{handle}/og.png):
// the 1200×630 card link previews show, with its name, platform, HuntScore,
// bounty range, total paid and scope, drawn in the dark theme's colours.
// The program's page names it as og:image where pages are indexed (the
// demo). Private programs have none, so nothing private is drawn.

const ogWidth, ogHeight = 1200, 630

// ogCard is what a share image says.
type ogCard struct {
	Name, Platform, Kind, Site string
	Tiles                      [4]ogTile
}

type ogTile struct{ Label, Value, Sub string }

// The dark theme's colours (00-theme.css).
var (
	ogText   = color.RGBA{0xe6, 0xed, 0xf7, 0xff}
	ogMuted  = color.RGBA{0x84, 0x94, 0xad, 0xff}
	ogAccent = color.RGBA{0xc4, 0xb5, 0xfd, 0xff}
	ogPurple = color.RGBA{0x7c, 0x3a, 0xed, 0xff}
	ogPanel  = color.RGBA{0x10, 0x18, 0x2c, 0xff}
	ogEdge   = color.RGBA{0x26, 0x35, 0x4f, 0xff}
)

// ogPlatformColour is a platform's tag colour in the dark theme.
func ogPlatformColour(name string) color.RGBA {
	switch name {
	case "hackerone":
		return color.RGBA{0x82, 0x9d, 0xff, 0xff}
	case "bugcrowd":
		return color.RGBA{0xff, 0x8f, 0x3d, 0xff}
	case "intigriti":
		return color.RGBA{0x2d, 0xd4, 0xbf, 0xff}
	case "yeswehack":
		return color.RGBA{0xff, 0x52, 0x77, 0xff}
	case "vulbox":
		return color.RGBA{0x34, 0xd3, 0x99, 0xff}
	case "issuehunt":
		return color.RGBA{0x38, 0xbd, 0xf8, 0xff}
	case "gobugfree":
		return color.RGBA{0xf8, 0x71, 0x71, 0xff}
	case "inspectiv":
		return color.RGBA{0xc0, 0x84, 0xfc, 0xff}
	}
	return color.RGBA{0xa3, 0xb1, 0xc2, 0xff}
}

// newOGCard is p's card. site names where it's shown ("huntdeck.io").
func newOGCard(p store.ProgramListItem, site string) ogCard {
	c := ogCard{Name: p.Name, Platform: p.Platform, Site: site, Kind: "Bug bounty program"}
	if !p.OffersBounty {
		c.Kind = "Vulnerability disclosure program"
	}
	if p.RemovedAt != nil {
		c.Kind += " · no longer listed"
	} else if p.State != "" && p.State != "open" {
		c.Kind += " · " + p.State
	}
	c.Tiles[0] = ogTile{"HuntScore", "—", "not scored yet"}
	if p.Hunt != nil {
		c.Tiles[0] = ogTile{"HuntScore", strconv.Itoa(int(p.Hunt.Score + 0.5)), p.Hunt.Confidence + " confidence"}
	}
	c.Tiles[1] = ogTile{"Bounty", "—", "range not shared"}
	if !p.OffersBounty {
		c.Tiles[1] = ogTile{"Bounty", "VDP", "no bounties"}
	} else if rng := amountRange(p.BountyMin, p.BountyMax, p.Currency); rng != "" {
		c.Tiles[1] = ogTile{"Bounty", rng, "per report"}
	}
	c.Tiles[2] = ogTile{"Total paid", "—", "not shared"}
	if p.PaidAll != nil {
		v := humanize.Money(*p.PaidAll, p.StatsCurrency)
		sub := "all time"
		if p.PaidAllEstimated {
			v, sub = "≈"+v, "all time, estimated"
		}
		c.Tiles[2] = ogTile{"Total paid", v, sub}
	}
	sub := ""
	switch {
	case p.Wildcards > 0:
		sub = strconv.Itoa(p.Wildcards) + plural(p.Wildcards, " wildcard", " wildcards")
	case p.Domains > 0:
		sub = strconv.Itoa(p.Domains) + plural(p.Domains, " domain", " domains")
	}
	c.Tiles[3] = ogTile{"In scope", strconv.Itoa(p.InScope) + plural(p.InScope, " asset", " assets"), sub}
	return c
}

// draw renders the card as a PNG.
func (c ogCard) draw() ([]byte, error) {
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	regular, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	f := ogFonts{bold: bold, regular: regular}
	img := image.NewRGBA(image.Rect(0, 0, ogWidth, ogHeight))
	fillGradient(img, color.RGBA{0x1b, 0x12, 0x3a, 0xff}, color.RGBA{0x0b, 0x12, 0x20, 0xff})
	const pad = 64

	// The logo, as the favicon draws it, and the name.
	fillRoundRect(img, image.Rect(pad, 52, pad+60, 112), 15, ogPurple)
	fillCircle(img, pad+30, 82, 17, color.White)
	fillCircle(img, pad+30, 82, 13, ogPurple)
	fillCircle(img, pad+30, 82, 5.5, color.White)
	for _, t := range []image.Rectangle{{image.Pt(pad+28, 59), image.Pt(pad+32, 66)}, {image.Pt(pad+28, 98), image.Pt(pad+32, 105)},
		{image.Pt(pad+7, 80), image.Pt(pad+14, 84)}, {image.Pt(pad+46, 80), image.Pt(pad+53, 84)}} {
		fillRoundRect(img, t, 1.5, color.White)
	}
	logo := f.face(true, 38)
	x := drawText(img, logo, pad+78, 96, "Hunt", ogText)
	drawText(img, logo, pad+78+x, 96, "Deck", ogAccent)

	// The platform, top right.
	pf := f.face(true, 26)
	label := platformLabel(c.Platform)
	w := textWidth(pf, label) + 64
	fillRoundRect(img, image.Rect(ogWidth-pad-w, 60, ogWidth-pad, 106), 23, ogEdge)
	fillRoundRect(img, image.Rect(ogWidth-pad-w+1, 61, ogWidth-pad-1, 105), 22, ogPanel)
	fillCircle(img, float32(ogWidth-pad-w+26), 83, 7, ogPlatformColour(c.Platform))
	drawText(img, pf, ogWidth-pad-w+42, 92, label, ogText)

	// The program's name: as big as fits on two lines.
	width := ogWidth - 2*pad
	var lines []string
	size := 80.0
	for ; ; size -= 8 {
		lines = wrapText(f.face(true, size), c.Name, width)
		if len(lines) <= 2 || size <= 48 {
			break
		}
	}
	name := f.face(true, size)
	if len(lines) > 2 {
		lines = append(lines[:1], fitText(name, strings.Join(lines[1:], " "), width))
	}
	y := 150 + int(size)
	for _, l := range lines {
		drawText(img, name, pad, y, fitText(name, l, width), ogText)
		y += int(size * 1.12)
	}
	drawText(img, f.face(false, 28), pad, y-int(size*1.12)+48, c.Kind, ogMuted)

	// The numbers.
	const gap, top, height = 20, 412, 136
	tw := (width - 3*gap) / 4
	lf, vf, sf := f.face(true, 17), f.face(true, 42), f.face(false, 20)
	for i, t := range c.Tiles {
		x := pad + i*(tw+gap)
		fillRoundRect(img, image.Rect(x, top, x+tw, top+height), 16, ogEdge)
		fillRoundRect(img, image.Rect(x+1, top+1, x+tw-1, top+height-1), 15, ogPanel)
		drawText(img, lf, x+22, top+38, strings.ToUpper(t.Label), ogMuted)
		v := vf
		if textWidth(v, t.Value) > tw-44 {
			v = f.face(true, 32)
		}
		drawText(img, v, x+22, top+88, fitText(v, t.Value, tw-44), ogText)
		drawText(img, sf, x+22, top+118, fitText(sf, t.Sub, tw-44), ogMuted)
	}

	foot := f.face(false, 22)
	drawText(img, foot, pad, ogHeight-36, "Live scope, changes and payouts", ogMuted)
	site := f.face(true, 22)
	drawText(img, site, ogWidth-pad-textWidth(site, c.Site), ogHeight-36, c.Site, ogAccent)

	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// programOGImage serves a public program's share image.
func (s *server) programOGImage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := s.Store.Program(ctx, canonicalPlatform(r.PathValue("platform")), r.PathValue("handle"))
	if err != nil || p.Private {
		http.NotFound(w, r)
		return
	}
	list, _, err := s.Store.Programs(ctx, store.ProgramFilter{ID: p.ID, Limit: 1})
	if err != nil || len(list) == 0 {
		s.serverError(w, r, err)
		return
	}
	site := "HuntDeck"
	if u, err := url.Parse(s.PublicURL); err == nil && u.Host != "" {
		site = u.Host
	}
	b, err := newOGCard(list[0], site).draw()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Security-Policy", imagePolicy)
	w.Header().Set("Cache-Control", "public, max-age=21600")
	_, _ = w.Write(b)
}

// siteImageAlt describes static/og.png.
const siteImageAlt = "HuntDeck: live scope, changes and payouts for public bug bounty programs"

// programImage is a program's share image, none for a private one.
func programImage(p store.ProgramRef) string {
	if p.Private {
		return ""
	}
	return programRefPath(p) + "/og.png"
}
