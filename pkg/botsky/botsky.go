package botsky

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/davhofer/indigo/api/atproto"
	"github.com/davhofer/indigo/api/bsky"
	lexutil "github.com/davhofer/indigo/lex/util"
	"github.com/davhofer/indigo/xrpc"
)

const DefaultServer = "https://bsky.social"

// TODO: need to wrap requests for rate limiting?

type Client struct {
	XrpcClient *xrpc.Client
	handle     string
	appkey     string
	// make sure only one auth refresher runs at a time
	refreshProcessLock sync.Mutex
	// read-write lock to ensure that concurrent processes can access the http auth information without problems
	authLock sync.RWMutex
}

// Sets up a new client connecting to the given server
func NewClient(ctx context.Context, server string, handle string, appkey string) (*Client, error) {
	client := &Client{
		XrpcClient: &xrpc.Client{
			Client: new(http.Client),
			Host:   server,
		},
		handle: handle,
		appkey: appkey,
	}
	return client, nil
}

func (c *Client) ResolveHandle(ctx context.Context, handle string) (string, error) {
	if strings.HasPrefix(handle, "@") {
		handle = handle[1:]
	}
	output, err := atproto.IdentityResolveHandle(ctx, c.XrpcClient, handle)
	if err != nil {
		return "", fmt.Errorf("ResolveHandle error: %v", err)
	}
	return output.Did, nil
}

func (c *Client) UpdateProfileDescription(ctx context.Context, description string) error {
	profileRecord, err := atproto.RepoGetRecord(ctx, c.XrpcClient, "", "app.bsky.actor.profile", c.handle, "self")
	if err != nil {
		return fmt.Errorf("UpdateProfileDescription error (RepoGetRecord): %v", err)
	}

	var actorProfile bsky.ActorProfile
	if err := DecodeRecordAsLexicon(profileRecord.Value, &actorProfile); err != nil {
		return fmt.Errorf("UpdateProfileDescription error (DecodeRecordAsLexicon): %v", err)
	}

	newProfile := bsky.ActorProfile{
		LexiconTypeID:        "app.bsky.actor.profile",
		Avatar:               actorProfile.Avatar,
		Banner:               actorProfile.Banner,
		CreatedAt:            actorProfile.CreatedAt,
		Description:          &description,
		DisplayName:          actorProfile.DisplayName,
		JoinedViaStarterPack: actorProfile.JoinedViaStarterPack,
		Labels:               actorProfile.Labels,
		PinnedPost:           actorProfile.PinnedPost,
	}

	input := atproto.RepoPutRecord_Input{
		Collection: "app.bsky.actor.profile",
		Record: &lexutil.LexiconTypeDecoder{
			Val: &newProfile,
		},
		Repo:       c.handle,
		Rkey:       "self",
		SwapRecord: profileRecord.Cid,
	}

	output, err := atproto.RepoPutRecord(ctx, c.XrpcClient, &input)
	if err != nil {
		return fmt.Errorf("UpdateProfileDescription error (RepoPutRecord): %v", err)
	}
	logger.Println("Profile updated:", output.Cid, output.Uri)
	return nil
}

// get posts from bsky API/AppView ***********************************************************

// TODO: method to get post directly from repo?

// Enriched post struct, including both the repo's FeedPost as well as bluesky's PostView
// Note: this fully relies on bsky api to be built
type RichPost struct {
	bsky.FeedPost

	AuthorDid   string // from *bsky.ActorDefs_ProfileViewBasic
	Cid         string
	Uri         string
	IndexedAt   string
	LikeCount   int64
	QuoteCount  int64
	ReplyCount  int64
	RepostCount int64
}

// Load bsky postViews from repo/user
// (This is not related to the number of views on a post)
func (c *Client) GetPostViews(ctx context.Context, handleOrDid string, limit int) ([]*bsky.FeedDefs_PostView, error) {
	// get all post uris
	postUris, err := c.RepoGetRecordUris(ctx, handleOrDid, "app.bsky.feed.post", limit)
	if err != nil {
		return nil, fmt.Errorf("GetPostViews error (RepoGetRecordUris): %v", err)
	}

	// hydrate'em
	postViews := make([]*bsky.FeedDefs_PostView, 0, len(postUris))
	for i := 0; i < len(postUris); i += 25 {
		j := i + 25
		if j > len(postUris) {
			j = len(postUris)
		}
		results, err := bsky.FeedGetPosts(ctx, c.XrpcClient, postUris[i:j])
		if err != nil {
			return nil, fmt.Errorf("GetPostViews error (FeedGetPosts): %v", err)
		}
		postViews = append(postViews, results.Posts...)
	}
	return postViews, nil
}

// Load enriched posts from repo/user
func (c *Client) GetPosts(ctx context.Context, handleOrDid string, limit int) ([]*RichPost, error) {
	postViews, err := c.GetPostViews(ctx, handleOrDid, limit)
	if err != nil {
		return nil, fmt.Errorf("GetPosts error (GetPostViews): %v", err)
	}

	posts := make([]*RichPost, 0, len(postViews))
	for _, postView := range postViews {
		var feedPost bsky.FeedPost
		if err := DecodeRecordAsLexicon(postView.Record, &feedPost); err != nil {
			return nil, fmt.Errorf("GetPosts error (DecodeRecordAsLexicon): %v", err)
		}
		posts = append(posts, &RichPost{
			FeedPost:    feedPost,
			AuthorDid:   postView.Author.Did,
			Cid:         postView.Cid,
			Uri:         postView.Uri,
			IndexedAt:   postView.IndexedAt,
			LikeCount:   *postView.LikeCount,
			QuoteCount:  *postView.QuoteCount,
			ReplyCount:  *postView.ReplyCount,
			RepostCount: *postView.RepostCount,
		})

	}
	return posts, nil
}

func (c *Client) GetPost(ctx context.Context, postUri string) (RichPost, error) {
	results, err := bsky.FeedGetPosts(ctx, c.XrpcClient, []string{postUri})
	if err != nil {
		return RichPost{}, fmt.Errorf("GetPost error (FeedGetPosts): %v", err)
	}
	if len(results.Posts) == 0 {
		return RichPost{}, fmt.Errorf("GetPost error: No post with the given uri found")
	}
	postView := results.Posts[0]

	var feedPost bsky.FeedPost
	err = DecodeRecordAsLexicon(postView.Record, &feedPost)
	if err != nil {
		return RichPost{}, fmt.Errorf("GetPost error (DecodeRecordAsLexicon): %v", err)
	}

	post := RichPost{
		FeedPost:    feedPost,
		AuthorDid:   postView.Author.Did,
		Cid:         postView.Cid,
		Uri:         postView.Uri,
		IndexedAt:   postView.IndexedAt,
		LikeCount:   *postView.LikeCount,
		QuoteCount:  *postView.QuoteCount,
		ReplyCount:  *postView.ReplyCount,
		RepostCount: *postView.RepostCount,
	}

	return post, nil
}
