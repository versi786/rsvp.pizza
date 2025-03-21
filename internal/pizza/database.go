package pizza

import (
	"strconv"
	"time"

	f "github.com/fauna/faunadb-go/v4/faunadb"
	"go.uber.org/zap"
)

var faunaClient *f.FaunaClient
var fridayCache *Cache[[]time.Time]
var positiveFriendCache *Cache[string]
var negativeFriendCache *Cache[bool]

func newFaunaClient(secret string, cacheTTL time.Duration) {
	faunaClient = f.NewFaunaClient(secret)
	fcache := NewCache(cacheTTL, GetUpcomingFridaysStr)
	fridayCache = &fcache
	posFriendCache := NewCache(24*time.Hour, GetFriendName)
	positiveFriendCache = &posFriendCache
	negFriendCache := NewCache[bool](5*time.Minute, nil)
	negativeFriendCache = &negFriendCache
}

func IsFriendAllowed(friendEmail string) (bool, error) {
	if negativeFriendCache.Has(friendEmail) {
		return false, nil
	}
	if positiveFriendCache.Has(friendEmail) {
		return true, nil
	}
	qRes, err := faunaClient.Query(
		f.Exists(f.MatchTerm(f.Index("all_emails"), friendEmail)),
	)
	if err != nil {
		Log.Error("fauna error", zap.Error(err))
		return false, err
	}
	var exists bool
	if err := qRes.Get(&exists); err != nil {
		Log.Error("fauna parse error", zap.Error(err))
		return false, err
	}
	if !exists {
		negativeFriendCache.Store(friendEmail, false)
	}
	return exists, nil
}

func GetCachedFriendName(friendEmail string) (string, error) {
	return positiveFriendCache.Get(friendEmail)
}

func GetFriendName(friendEmail string) (string, error) {
	/*
		Get(Select(
			"ref",
			Get(Match(Index("all_emails"), "test@email.com"))
		))
	*/
	var name string
	qRes, err := faunaClient.Query(f.Get(f.MatchTerm(f.Index("all_emails"), friendEmail)))
	if err != nil {
		Log.Error("fauna error", zap.Error(err))
		return name, err
	}
	if err = qRes.At(f.ObjKey("data", "name")).Get(&name); err != nil {
		Log.Error("fauna decode error", zap.Error(err))
		return name, err
	}
	return name, nil
}

func GetAllFridays() ([]time.Time, error) {
	qRes, err := faunaClient.Query(f.Paginate(f.Match(f.Index("all_fridays"))))
	if err != nil {
		Log.Error("fauna error", zap.Error(err))
		return nil, err
	}
	var arr []time.Time
	if err = qRes.At(f.ObjKey("data")).Get(&arr); err != nil {
		Log.Error("fauna decode error", zap.Error(err))
		return nil, err
	}
	Log.Debug("got all fridays", zap.Times("fridays", arr))
	return arr, nil
}

func GetCachedFridays(daysAhead int) ([]time.Time, error) {
	return fridayCache.Get(strconv.Itoa(daysAhead))
}

func GetUpcomingFridaysStr(daysAhead string) ([]time.Time, error) {
	days, err := strconv.ParseInt(daysAhead, 10, 32)
	if err != nil {
		return nil, err
	}
	return GetUpcomingFridays(int(days))
}

func GetUpcomingFridays(daysAhead int) ([]time.Time, error) {
	/*
		Map(
			Paginate(
				Range(
					Match(Index("all_fridays_range")),
					Now(),
					TimeAdd(TimeAdd(Now(), 1, "day"), 30, "days")
				)
			),
			Lambda('x', Select(0, Var('x')))
		)
	*/
	qRes, err := faunaClient.Query(f.Map(f.Paginate(f.Range(
		f.Match(f.Index("all_fridays_range")),
		f.Now(),
		f.TimeAdd(f.TimeAdd(f.Now(), 1, "days"), daysAhead, "days"),
	)), f.Lambda("x", f.Select(0, f.Var("x")))))
	if err != nil {
		Log.Error("fauna error", zap.Error(err))
		return nil, err
	}
	var times []time.Time
	if err = qRes.At(f.ObjKey("data")).Get(&times); err != nil {
		Log.Error("fauna decode error", zap.Error(err))
		return nil, err
	}

	Log.Debug("got all fridays", zap.Times("fridays", times))

	return times, nil
}

func CreateRSVP(friendEmail, code string, pendingDates []time.Time) error {
	qRes, err := faunaClient.Query(
		f.Update(
			f.Select(
				"ref",
				f.Get(f.MatchTerm(f.Index("all_emails"), friendEmail)),
			),
			f.Obj{"data": f.Obj{
				"pending_rsvps": pendingDates,
				"rsvp_code":     code,
			}},
		),
	)
	if err != nil {
		Log.Error("fauna error", zap.Error(err))
		return err
	}
	Log.Debug("rsvp created: %+v", zap.Any("result", qRes))
	return nil
}

func ConfirmRSVP(friendEmail, code string) error {
	qRes, err := faunaClient.Query(
		f.Let().Bind(
			"pending", f.Select([]string{"data", "pending_rsvps"},
				f.Get(f.MatchTerm(f.Index("rsvp_codes"), []string{friendEmail, code}))),
		).Bind(
			"ref", f.Select("ref",
				f.Get(f.MatchTerm(f.Index("rsvp_codes"), []string{friendEmail, code}))),
		).In(
			f.Update(f.Var("ref"), f.Obj{
				"data": f.Obj{
					"confirmed_rsvps": f.Var("pending"),
				},
			}),
		),
	)
	if err != nil {
		Log.Error("fauna error", zap.Error(err))
		return err
	}
	Log.Debug("rsvp confirmed", zap.Any("result", qRes))
	return nil
}
