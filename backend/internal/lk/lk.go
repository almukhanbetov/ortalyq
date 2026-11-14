// Package lk — тонкая обёртка над LiveKit server SDK: токены, управление комнатой и запись (Egress).
package lk

import (
	"context"
	"net/http"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

type Client struct {
	apiKey, apiSecret string
	rooms             *lksdk.RoomServiceClient
	egress            *lksdk.EgressClient
}

func New(url, key, secret string) *Client {
	return &Client{
		apiKey:    key,
		apiSecret: secret,
		rooms:     lksdk.NewRoomServiceClient(url, key, secret),
		egress:    lksdk.NewEgressClient(url, key, secret),
	}
}

func RoomName(lessonID string) string { return "lesson-" + lessonID }

// JoinToken выдаёт токен доступа только к одной комнате занятия.
func (c *Client) JoinToken(room, identity, name string, ttl time.Duration) (string, error) {
	t := auth.NewAccessToken(c.apiKey, c.apiSecret).
		SetIdentity(identity).
		SetName(name).
		SetValidFor(ttl).
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin:       true,
			Room:           room,
			CanPublish:     ptr(true),
			CanSubscribe:   ptr(true),
			CanPublishData: ptr(true),
		})
	return t.ToJWT()
}

func ptr[T any](v T) *T { return &v }

func (c *Client) DeleteRoom(ctx context.Context, room string) error {
	_, err := c.rooms.DeleteRoom(ctx, &livekit.DeleteRoomRequest{Room: room})
	return err
}

// RoomNames — имена существующих комнат LiveKit.
func (c *Client) RoomNames(ctx context.Context) ([]string, error) {
	res, err := c.rooms.ListRooms(ctx, &livekit.ListRoomsRequest{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(res.Rooms))
	for _, r := range res.Rooms {
		names = append(names, r.Name)
	}
	return names, nil
}

type S3Target struct {
	AccessKey, Secret, Endpoint, Bucket, Region string
}

// StartRecording запускает RoomComposite Egress с нашим шаблоном (доска + экран + видео + звук)
// и прямой загрузкой MP4 в S3.
func (c *Client) StartRecording(ctx context.Context, room, templateURL, objectKey string, s3 S3Target) (*livekit.EgressInfo, error) {
	req := &livekit.RoomCompositeEgressRequest{
		RoomName:      room,
		Layout:        "lesson",
		CustomBaseUrl: templateURL,
		Options:       &livekit.RoomCompositeEgressRequest_Preset{Preset: livekit.EncodingOptionsPreset_H264_720P_30},
		FileOutputs: []*livekit.EncodedFileOutput{{
			FileType:        livekit.EncodedFileType_MP4,
			Filepath:        objectKey,
			DisableManifest: true,
			Output: &livekit.EncodedFileOutput_S3{S3: &livekit.S3Upload{
				AccessKey:      s3.AccessKey,
				Secret:         s3.Secret,
				Endpoint:       s3.Endpoint,
				Bucket:         s3.Bucket,
				Region:         s3.Region,
				ForcePathStyle: true,
			}},
		}},
	}
	return c.egress.StartRoomCompositeEgress(ctx, req)
}

func (c *Client) StopRecording(ctx context.Context, egressID string) (*livekit.EgressInfo, error) {
	return c.egress.StopEgress(ctx, &livekit.StopEgressRequest{EgressId: egressID})
}

func (c *Client) GetEgress(ctx context.Context, egressID string) (*livekit.EgressInfo, error) {
	res, err := c.egress.ListEgress(ctx, &livekit.ListEgressRequest{EgressId: egressID})
	if err != nil {
		return nil, err
	}
	if len(res.Items) == 0 {
		return nil, nil
	}
	return res.Items[0], nil
}

func (c *Client) ReceiveWebhook(r *http.Request) (*livekit.WebhookEvent, error) {
	return webhook.ReceiveWebhookEvent(r, auth.NewSimpleKeyProvider(c.apiKey, c.apiSecret))
}
