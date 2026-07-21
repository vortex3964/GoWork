package messagearea

import (
	"fmt"
	"image/color"

	"charm.land/glamour/v2/ansi"

	"GoWork/Tui/Style"
)

func markdownStyle() ansi.StyleConfig {
	return ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: mdColor(style.Text),
			},
			Margin: mdUint(0),
		},
		Paragraph: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: mdColor(style.Text),
			},
		},
		BlockQuote: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color:  mdColor(style.Muted),
				Italic: mdBool(true),
			},
			Indent:      mdUint(1),
			IndentToken: mdStr("│ "),
		},
		List: ansi.StyleList{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color: mdColor(style.Text),
				},
			},
			LevelIndent: 2,
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				BlockSuffix: "\n",
				Color:       mdColor(style.Primary),
				Bold:        mdBool(true),
			},
		},
		H1: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          " ",
				Suffix:          " ",
				Color:           mdColor(style.Secondary),
				BackgroundColor: mdColor(style.Primary),
				Bold:            mdBool(true),
			},
		},
		H2: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "▌ ",
				Color:  mdColor(style.Primary),
				Bold:   mdBool(true),
			},
		},
		H3: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "┃ ",
				Color:  mdColor(style.Primary),
			},
		},
		H4: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "│ ",
				Color:  mdColor(style.Primary),
			},
		},
		H5: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "┆ ",
				Color:  mdColor(style.Muted),
			},
		},
		H6: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix: "┊ ",
				Color:  mdColor(style.Muted),
			},
		},
		Strikethrough: ansi.StylePrimitive{
			CrossedOut: mdBool(true),
			Color:      mdColor(style.Muted),
		},
		Emph: ansi.StylePrimitive{
			Italic: mdBool(true),
			Color:  mdColor(style.Special),
		},
		Strong: ansi.StylePrimitive{
			Bold:  mdBool(true),
			Color: mdColor(style.Warning),
		},
		HorizontalRule: ansi.StylePrimitive{
			Color:  mdColor(style.Muted),
			Format: "\n──────\n",
		},
		Item: ansi.StylePrimitive{
			BlockPrefix: "• ",
			Color:       mdColor(style.Primary),
		},
		Enumeration: ansi.StylePrimitive{
			BlockPrefix: ". ",
			Color:       mdColor(style.Primary),
		},
		Task: ansi.StyleTask{
			Ticked:   "✓ ",
			Unticked: "◯ ",
		},
		Link: ansi.StylePrimitive{
			Color:     mdColor(style.Info),
			Underline: mdBool(true),
		},
		LinkText: ansi.StylePrimitive{
			Color: mdColor(style.Special),
			Bold:  mdBool(true),
		},
		Image: ansi.StylePrimitive{
			Color:     mdColor(style.Info),
			Underline: mdBool(true),
		},
		ImageText: ansi.StylePrimitive{
			Color:  mdColor(style.Muted),
			Format: "Image: {{.text}} →",
		},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          "\u00a0",
				Suffix:          "\u00a0",
				Color:           mdColor(style.Special),
				BackgroundColor: mdColor(style.Highlight),
			},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color: mdColor(style.Text),
				},
				Margin: mdUint(2),
			},
			Chroma: &ansi.Chroma{
				Text:                ansi.StylePrimitive{Color: mdColor(style.Text)},
				Background:          ansi.StylePrimitive{BackgroundColor: mdColor(style.Highlight)},
				Comment:             ansi.StylePrimitive{Color: mdColor(style.Muted), Italic: mdBool(true)},
				CommentPreproc:      ansi.StylePrimitive{Color: mdColor(style.Special)},
				Keyword:             ansi.StylePrimitive{Color: mdColor(style.Info)},
				KeywordReserved:     ansi.StylePrimitive{Color: mdColor(style.Info)},
				KeywordNamespace:    ansi.StylePrimitive{Color: mdColor(style.Info)},
				KeywordType:         ansi.StylePrimitive{Color: mdColor(style.Special)},
				Operator:            ansi.StylePrimitive{Color: mdColor(style.Danger)},
				Punctuation:         ansi.StylePrimitive{Color: mdColor(style.Text)},
				Name:                ansi.StylePrimitive{Color: mdColor(style.Text)},
				NameBuiltin:         ansi.StylePrimitive{Color: mdColor(style.Special)},
				NameTag:             ansi.StylePrimitive{Color: mdColor(style.Info)},
				NameAttribute:       ansi.StylePrimitive{Color: mdColor(style.Warning)},
				NameClass:           ansi.StylePrimitive{Color: mdColor(style.Primary), Bold: mdBool(true)},
				NameConstant:        ansi.StylePrimitive{Color: mdColor(style.Special)},
				NameDecorator:       ansi.StylePrimitive{Color: mdColor(style.Warning)},
				NameFunction:        ansi.StylePrimitive{Color: mdColor(style.Warning)},
				LiteralNumber:       ansi.StylePrimitive{Color: mdColor(style.Special)},
				LiteralString:       ansi.StylePrimitive{Color: mdColor(style.Success)},
				LiteralStringEscape: ansi.StylePrimitive{Color: mdColor(style.Warning)},
				GenericDeleted:      ansi.StylePrimitive{Color: mdColor(style.Danger)},
				GenericInserted:     ansi.StylePrimitive{Color: mdColor(style.Success)},
				GenericEmph:         ansi.StylePrimitive{Italic: mdBool(true)},
				GenericStrong:       ansi.StylePrimitive{Bold: mdBool(true)},
				GenericSubheading:   ansi.StylePrimitive{Color: mdColor(style.Muted)},
				Error: ansi.StylePrimitive{
					Color:           mdColor(style.Text),
					BackgroundColor: mdColor(style.Danger),
				},
			},
		},
		Table: ansi.StyleTable{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color: mdColor(style.Text),
				},
			},
			CenterSeparator: mdStr("┼"),
			ColumnSeparator: mdStr("│"),
			RowSeparator:    mdStr("─"),
		},
		DefinitionDescription: ansi.StylePrimitive{
			BlockPrefix: "\n→ ",
			Color:       mdColor(style.Muted),
		},
	}
}

func mdStr(s string) *string { return &s }
func mdBool(b bool) *bool    { return &b }
func mdUint(u uint) *uint    { return &u }

func mdColor(c color.Color) *string {
	r, g, b, _ := c.RGBA()
	s := fmt.Sprintf("#%02X%02X%02X", uint8(r>>8), uint8(g>>8), uint8(b>>8))
	return &s
}
