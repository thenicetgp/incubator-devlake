package tasks

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/merico-dev/lake/logger"
	"github.com/merico-dev/lake/plugins/core"
	githubUtils "github.com/merico-dev/lake/plugins/github/utils"
	"github.com/merico-dev/lake/utils"
)

type GithubApiClient struct {
	tokenIndex int
	// This is for multiple token functionality so we can loop through an array of tokens.
	tokens []string
	core.ApiClient
}

func NewGithubApiClient(endpoint string, tokens []string, ctx context.Context) *GithubApiClient {
	githubApiClient := &GithubApiClient{}
	githubApiClient.tokenIndex = 0
	githubApiClient.tokens = tokens
	// Rotates token on each request.
	githubApiClient.SetBeforeFunction(func(req *http.Request) error {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", githubApiClient.tokens[githubApiClient.tokenIndex]))
		// Set next token index
		githubApiClient.tokenIndex = (githubApiClient.tokenIndex + 1) % len(githubApiClient.tokens)
		return nil
	})
	githubApiClient.Setup(
		endpoint,
		map[string]string{},
		10*time.Second,
		3,
	)
	if ctx != nil {
		githubApiClient.SetContext(ctx)
	}
	return githubApiClient
}

type GithubPaginationHandler func(res *http.Response) error
type GithubSearchPaginationHandler func(res *http.Response) (int, error)

// run all requests in an Ants worker pool
// conc - number of concurent requests you want to run
func (githubApiClient *GithubApiClient) FetchPages(path string, queryParams *url.Values, pageSize int,
	scheduler *utils.WorkerScheduler, handler GithubPaginationHandler) error {
	if queryParams == nil {
		queryParams = &url.Values{}
	}

	queryParams.Set("page", strconv.Itoa(1))
	queryParams.Set("per_page", strconv.Itoa(pageSize))
	res, err := githubApiClient.Get(path, queryParams, nil)
	if err != nil {
		return err
	}
	handlerErr := handler(res)
	if handlerErr != nil {
		return handlerErr
	}
	linkHeader := res.Header.Get("Link")
	if linkHeader == "" {
		return nil
	}
	paginationInfo2, getPagingErr := githubUtils.GetPagingFromLinkHeader(linkHeader)
	if getPagingErr != nil {
		logger.Info("GetPagingFromLinkHeader err: ", getPagingErr)
	}
	pages := paginationInfo2.Last

	for i := 2; i <= pages; i++ {
		page := i
		err = scheduler.Submit(func() error {
			queryParams.Set("page", strconv.Itoa(page))
			queryParams.Set("per_page", strconv.Itoa(pageSize))
			res, err := githubApiClient.Get(path, queryParams, nil)
			if err != nil {
				return err
			}
			handlerErr = handler(res)
			if handlerErr != nil {
				return handlerErr
			}
			return nil
		})
		if err != nil {
			return err
		}
	}

	scheduler.WaitUntilFinish()
	return nil
}
