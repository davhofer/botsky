// This file has been heavily modified from its original version.
// Original source: https://github.com/danrusei/gobot-bsky/blob/main/post.go
// License: Apache 2.0

package botsky

import (
	"context"
	"fmt"
	"net/url"
    "regexp"
	"time"

	"github.com/davhofer/indigo/api/atproto"
	"github.com/davhofer/indigo/api/bsky"
	lexutil "github.com/davhofer/indigo/lex/util"
)

// TODO: embed videos

type Facet_Type int

const (
	Facet_Link Facet_Type = iota + 1
	Facet_Mention
	Facet_Tag
)

const domainRegex = `[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*\.[a-zA-Z]{2,10}`


type InlineLink struct {
	Text string // a substring of the post text which will be clickable as a link
	Url  string // the link url
}

type RecordRef struct {
	Cid string
	Uri string
}

type Embed struct {
	Link           Link
	Images         []ImageSourceParsed
	UploadedImages []lexutil.LexBlob
	Record         RecordRef
}

type ReplyReference struct {
	Uri     string
	Cid     string
	RootUri string
	RootCid string
}

type Link struct {
	Title       string
	Uri         url.URL
	Description string
	Thumb       lexutil.LexBlob
}

type ImageSource struct {
	Alt string
	Uri string
}
type ImageSourceParsed struct {
	Alt string
	Uri url.URL
}

func (c *Client) Repost(ctx context.Context, postUri string) (string, string, error) {

	_, cid, err := c.RepoGetPostAndCid(ctx, postUri)
	if err != nil {
		return "", "", fmt.Errorf("Error getting post to repost: %v", err)
	}
	ref := atproto.RepoStrongRef{
		Uri: postUri,
		Cid: cid,
	}

	post := bsky.FeedRepost{
		LexiconTypeID: "app.bsky.feed.repost",
		CreatedAt:     time.Now().Format(time.RFC3339),
		Subject:       &ref,
	}

	post_input := &atproto.RepoCreateRecord_Input{
		Collection: "app.bsky.feed.repost",
		Repo:       c.XrpcClient.Auth.Did,
		Record:     &lexutil.LexiconTypeDecoder{Val: &post},
	}
	response, err := atproto.RepoCreateRecord(ctx, c.XrpcClient, post_input)
	if err != nil {
		return "", "", fmt.Errorf("unable to repost: %v", err)
	}

	return response.Cid, response.Uri, nil
}

type PostBuilder struct {
	Text             string
	AdditionalTags   []string
    InlineLinks      []InlineLink
	Languages        []string
	ReplyUri         string
	EmbedLink        string
	EmbedImages      []ImageSource
	EmbedPostQuote   string
}

func NewPostBuilder(text string) *PostBuilder {
	pb := &PostBuilder{
		Text:             text,
	}

	return pb
}

func (pb *PostBuilder) AddTags(tags []string) *PostBuilder {
	pb.AdditionalTags = append(pb.AdditionalTags, tags...)
	return pb
}

func (pb *PostBuilder) AddInlineLinks(links []InlineLink) *PostBuilder {
    pb.InlineLinks = append(pb.InlineLinks, links...)
	return pb
}

func (pb *PostBuilder) AddLanguage(language string) *PostBuilder {
	pb.Languages = append(pb.Languages, language)
	return pb
}

func (pb *PostBuilder) ReplyTo(postUri string) *PostBuilder {
	pb.ReplyUri = postUri
	return pb
}

func (pb *PostBuilder) AddEmbedLink(link string) *PostBuilder {
	pb.EmbedLink = link
	return pb
}

func (pb *PostBuilder) AddImages(images []ImageSource) *PostBuilder {
	pb.EmbedImages = append(pb.EmbedImages, images...)
	return pb
}

func (pb *PostBuilder) AddQuotedPost(postUri string) *PostBuilder {
	pb.EmbedPostQuote = postUri
	return pb
}


func (c *Client) Post(ctx context.Context, pb *PostBuilder) (string, string, error) {
	nEmbeds := 0
	if pb.EmbedImages != nil {
		nEmbeds++
	}
	if pb.EmbedLink != "" {
		nEmbeds++
	}
	if pb.EmbedPostQuote != "" {
		nEmbeds++
	}

	if nEmbeds > 1 {
		return "", "", fmt.Errorf("Can only include one type of Embed (images, embedded link, quoted post) in posts.")
	}
    var embed Embed


	if len(pb.Languages) == 0 {
		pb.Languages = []string{"en"}
	}
    // prepare embeds
	if pb.EmbedImages != nil {
		var parsedImages []ImageSourceParsed
		for _, img := range pb.EmbedImages {
			parsedUrl, err := url.Parse(img.Uri)
			if err != nil {
				return "", "", fmt.Errorf("Unable to parse image source uri: %s", img.Uri)
			} else {
				parsedImages = append(parsedImages, ImageSourceParsed{Alt: img.Alt, Uri: *parsedUrl})
			}
		}
		if len(parsedImages) > 0 {
			blobs, err := c.RepoUploadImages(ctx, parsedImages)
			if err != nil {
				return "", "", fmt.Errorf("Error when uploading images: %v", err)
			}
			embed.Images = parsedImages
			embed.UploadedImages = blobs
		}
	}

	if pb.EmbedLink != "" {
		parsedLink, err := url.Parse(pb.EmbedLink)
		if err != nil {
			return "", "", fmt.Errorf("Error when parsing link: %v", err)
		}

		siteTags, err := fetchOpenGraphTwitterTags(pb.EmbedLink)
		if err != nil {
			return "", "", fmt.Errorf("Error when fetching og/twitter tags from link: %v", err)
		}

		title := siteTags["title"]
		description := siteTags["description"]
		imageUrl, hasImage := siteTags["image"]
		alt := siteTags["image:alt"]

		var blob lexutil.LexBlob
		if hasImage {
			parsedImageUrl, err := url.Parse(imageUrl)
			if err != nil {
				return "", "", fmt.Errorf("Error when parsing image url: %v", err)
			}
			previewImg := ImageSourceParsed{
				Uri: *parsedImageUrl,
				Alt: alt,
			}
			b, err := c.RepoUploadImage(ctx, previewImg)
			if err != nil {
				return "", "", fmt.Errorf("Error when trying to upload image: %v", err)
			}
			if b != nil {
				blob = *b
			}
		}

		embed.Link.Title = title
		embed.Link.Uri = *parsedLink
		embed.Link.Description = description
		embed.Link.Thumb = blob
	}

	if pb.EmbedPostQuote != "" {
		_, cid, err := c.RepoGetPostAndCid(ctx, pb.EmbedPostQuote)
		if err != nil {
			return "", "", fmt.Errorf("Error when getting quoted post: %v", err)
		}
		embed.Record.Cid = cid
		embed.Record.Uri = pb.EmbedPostQuote
	}

    var replyReference ReplyReference
	if pb.ReplyUri != "" {
		replyPost, cid, err := c.RepoGetPostAndCid(ctx, pb.ReplyUri)
		if err != nil {
			return "", "", fmt.Errorf("Error when getting reply post: %v", err)
		}

		var rootCid, rootUri string
		if replyPost.Reply != nil && *replyPost.Reply != (bsky.FeedPost_ReplyRef{}) {
			rootCid = replyPost.Reply.Root.Cid
			rootUri = replyPost.Reply.Root.Uri
		} else {
			rootCid = cid
			rootUri = pb.ReplyUri
		}

		replyReference = ReplyReference{
			Uri:     pb.ReplyUri,
			Cid:     cid,
			RootUri: rootUri,
			RootCid: rootCid,
		}
	}

    // parse mentions
    mentionRegex := `[^a-zA-Z0-9](@` + domainRegex + `)`
    re := regexp.MustCompile(mentionRegex)
	matches := re.FindAllStringSubmatchIndex(pb.Text, -1)


    var mentionMatches []struct{Value string; Start int; End int; Did string}
    for _, m := range matches {
        start := m[2]
        end := m[3]
        value := pb.Text[start:end] 
        // cut off the @
        handle := value[1:]
        resolveOutput, err := atproto.IdentityResolveHandle(ctx, c.XrpcClient, handle)
        if err != nil {
            // cannot resolve handle => not a mention
            continue 
        }
        mentionMatches = append(mentionMatches, struct{Value string; Start int; End int; Did string}{
            Value: handle,
            Start: start,
            End: end,
            Did: resolveOutput.Did,
        })
    }


	// Build post
    post, err := buildPost(pb, embed, replyReference, mentionMatches)
	if err != nil {
		return "", "", fmt.Errorf("Error when building post: %v", err)
	}

	return c.RepoCreatePostRecord(ctx, post)

}

// Build the request
func buildPost(pb *PostBuilder, embed Embed, replyReference ReplyReference, mentionMatches []struct{Value string; Start int; End int; Did string}) (bsky.FeedPost, error) {
	post := bsky.FeedPost{Langs: pb.Languages}

	post.Text = pb.Text
	post.LexiconTypeID = "app.bsky.feed.post"
	post.CreatedAt = time.Now().Format(time.RFC3339)
	post.Tags = pb.AdditionalTags

	// RichtextFacet Section
	// https://docs.bsky.app/docs/advanced-guides/post-richtext

	Facets := []*bsky.RichtextFacet{}

    // mentions
    for _, match := range mentionMatches {
		facet := &bsky.RichtextFacet{}
		features := []*bsky.RichtextFacet_Features_Elem{}
        feature := &bsky.RichtextFacet_Features_Elem{
            RichtextFacet_Mention: &bsky.RichtextFacet_Mention{
                LexiconTypeID: Facet_Mention.String(),
                Did: match.Did,
            },
        }
		features = append(features, feature)
		facet.Features = features

		index := &bsky.RichtextFacet_ByteSlice{
			ByteStart: int64(match.Start),
			ByteEnd:   int64(match.End),
		}
		facet.Index = index

		Facets = append(Facets, facet)
    }

    // user-provided inline links
    for _, link := range pb.InlineLinks {
		facet := &bsky.RichtextFacet{}
		features := []*bsky.RichtextFacet_Features_Elem{}
        feature := &bsky.RichtextFacet_Features_Elem{
            RichtextFacet_Link: &bsky.RichtextFacet_Link{
                LexiconTypeID: Facet_Link.String(),
                Uri:           link.Url,
            },
        }
		features = append(features, feature)
		facet.Features = features

		ByteStart, ByteEnd, err := findSubstring(post.Text, link.Text)
		if err != nil {
			return post, fmt.Errorf("Unable to find the substring: %v , %v", Facet_Link, err)
		}

		index := &bsky.RichtextFacet_ByteSlice{
			ByteStart: int64(ByteStart),
			ByteEnd:   int64(ByteEnd),
		}
		facet.Index = index

		Facets = append(Facets, facet)
    }

    // auto-detect inline links
    urlRegex := `https?:\/\/` + domainRegex + `(\/(` + domainRegex + `)+)*\/?`
    matches := findRegexMatches(pb.Text, urlRegex)
    for _, match := range matches {
		facet := &bsky.RichtextFacet{}
		features := []*bsky.RichtextFacet_Features_Elem{}
        feature := &bsky.RichtextFacet_Features_Elem{
            RichtextFacet_Link: &bsky.RichtextFacet_Link{
                LexiconTypeID: Facet_Link.String(),
                Uri: match.Value,
            },
        }
		features = append(features, feature)
		facet.Features = features

		index := &bsky.RichtextFacet_ByteSlice{
			ByteStart: int64(match.Start),
			ByteEnd:   int64(match.End),
		}
		facet.Index = index

		Facets = append(Facets, facet)

    }

    // hashtags
    hashtagRegex := `(?:^|\s)(#[^\d\s]\S*)`
    matches = findRegexMatches(post.Text, hashtagRegex)
    for _, m := range matches {
        facet := &bsky.RichtextFacet{}
        features := []*bsky.RichtextFacet_Features_Elem{}
        feature := &bsky.RichtextFacet_Features_Elem{}

        feature = &bsky.RichtextFacet_Features_Elem{
            RichtextFacet_Tag: &bsky.RichtextFacet_Tag{
                LexiconTypeID: Facet_Tag.String(),
                Tag:           stripHashtag(m.Value),
            },
        }

        features = append(features, feature)
        facet.Features = features

        index := &bsky.RichtextFacet_ByteSlice{
            ByteStart: int64(m.Start),
            ByteEnd:   int64(m.End),
        }
        facet.Index = index

        Facets = append(Facets, facet)
	}

	post.Facets = Facets

	var FeedPost_Embed bsky.FeedPost_Embed
	embedFlag := true

	// Embed Section (either external links or images)
	// As of now it allows only one Embed type per post:
	// https://github.com/bluesky-social/indigo/blob/main/api/bsky/feedpost.go
	if embed.Link != (Link{}) {

		FeedPost_Embed.EmbedExternal = &bsky.EmbedExternal{
			LexiconTypeID: "app.bsky.embed.external",
			External: &bsky.EmbedExternal_External{
				Title:       embed.Link.Title,
				Uri:         embed.Link.Uri.String(),
				Description: embed.Link.Description,
				Thumb:       &embed.Link.Thumb,
			},
		}

	} else if len(embed.Images) != 0 && len(embed.Images) == len(embed.UploadedImages) {

		EmbedImages := bsky.EmbedImages{
			LexiconTypeID: "app.bsky.embed.images",
			Images:        make([]*bsky.EmbedImages_Image, len(embed.Images)),
		}

		for i, img := range embed.Images {
			EmbedImages.Images[i] = &bsky.EmbedImages_Image{
				Alt:   img.Alt,
				Image: &embed.UploadedImages[i],
			}
		}

		FeedPost_Embed.EmbedImages = &EmbedImages

	} else if embed.Record != (RecordRef{}) {
		EmbedRecord := bsky.EmbedRecord{
			LexiconTypeID: "app.bsky.embed.record",
			Record: &atproto.RepoStrongRef{
				LexiconTypeID: "com.atproto.repo.strongRef",
				Cid:           embed.Record.Cid,
				Uri:           embed.Record.Uri,
			},
		}

		FeedPost_Embed.EmbedRecord = &EmbedRecord
	} else {
		embedFlag = false
	}

	// avoid error when trying to marshal empty field (*bsky.FeedPost_Embed)
	if embedFlag {
		post.Embed = &FeedPost_Embed
	}

	// set reply
	if replyReference != (ReplyReference{}) {
		post.Reply = &bsky.FeedPost_ReplyRef{
			Parent: &atproto.RepoStrongRef{
				Uri: replyReference.Uri,
				Cid: replyReference.Cid,
			},
			Root: &atproto.RepoStrongRef{
				Uri: replyReference.RootUri,
				Cid: replyReference.RootCid,
			},
		}
	}

	return post, nil
}

func (f Facet_Type) String() string {
	switch f {
	case Facet_Link:
		return "app.bsky.richtext.facet#link"
	case Facet_Mention:
		return "app.bsky.richtext.facet#mention"
	case Facet_Tag:
		return "app.bsky.richtext.facet#tag"
	default:
		return "Unknown"
	}
}
