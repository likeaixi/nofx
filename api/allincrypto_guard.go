package api

import (
	"errors"
	"nofx/planning"
	"nofx/store"
	"nofx/trader/allincrypto"
	"sort"
)

func (s *Server) guardCopyMutation(user, id string) error {
	var row store.Trader
	if err := s.store.GormDB().Where("user_id = ? AND id = ?", user, id).First(&row).Error; err != nil {
		return err
	}
	outstanding, err := allincrypto.Outstanding(s.store.GormDB(), id)
	if err != nil {
		return err
	}
	if outstanding {
		return errors.New("AllinCrypto has followed positions or unresolved orders; preserve it for reconciliation")
	}
	strategy, _ := s.store.Strategy().Get(user, row.StrategyID)
	if !strategy.IsAllinCrypto() {
		return nil
	}
	if row.IsRunning {
		return errors.New("stop AllinCrypto and resolve its followed exposure before changing or deleting it")
	}
	if s.traderManager != nil {
		if runtime, e := s.traderManager.GetTrader(id); e == nil {
			if running, _ := runtime.GetStatus()["is_running"].(bool); running {
				return errors.New("AllinCrypto is still running or draining orders")
			}
		}
	}

	return nil
}
func (s *Server) guardCopyStrategyMutation(user, strategy string) error {
	traders, err := s.store.Trader().List(user)
	if err != nil {
		return err
	}
	for _, tr := range traders {
		if tr.StrategyID == strategy {
			if err = s.guardCopyMutation(user, tr.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) guardCopyExchangeMutation(user, exchange string) error {
	traders, err := s.store.Trader().List(user)
	if err != nil {
		return err
	}
	for _, tr := range traders {
		if tr.ExchangeID == exchange {
			if err = s.guardCopyMutation(user, tr.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Strategy edits must not race start/reload of one of their bound traders.
func (s *Server) lockStrategyTraders(user, strategy string) (func(), error) {
	traders, err := s.store.Trader().List(user)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, tr := range traders {
		if tr.StrategyID == strategy {
			ids = append(ids, tr.ID)
		}
	}
	sort.Strings(ids)
	var release []func()
	for _, id := range ids {
		release = append(release, planning.LockAccount("trader-mutation:"+id))
	}
	// Also cover a trader attached after the initial binding snapshot.
	release = append(release, planning.LockAccount("strategy-mutation:"+strategy))
	return func() {
		for i := len(release) - 1; i >= 0; i-- {
			release[i]()
		}
	}, nil
}
