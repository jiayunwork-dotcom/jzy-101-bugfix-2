package store

import "waveguide-service/internal/domain"

// builtinProfiles 内置样例档案：WR-90，业内常见的 X 波段标准矩形波导。
// 宽边 0.9 in = 22.86 mm，窄边 0.4 in = 10.16 mm，空气填充。
// 主模 TE10 截止频率约 6.557 GHz，次低模 TE20 约 13.115 GHz、
// TE01 约 14.754 GHz，因此在常用工作频段 8.2–12.4 GHz 内
// 主模明确落在单模传播区间，可直接用于核对服务输出。
func builtinProfiles() []domain.Profile {
	return []domain.Profile{
		{
			Name: "WR-90",
			Geometry: domain.Geometry{
				BroadDimension:  0.02286,
				NarrowDimension: 0.01016,
				RelPermittivity: 1.0,
				RelPermeability: 1.0,
			},
		},
	}
}
