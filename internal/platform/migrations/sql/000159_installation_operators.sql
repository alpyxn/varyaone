-- Kurulum operatörleri: yedekleme ve geri yükleme yetkisini firma rolünden ayır.
--
-- Sorun: `system.backup.manage` bir FİRMA izniydi ve her firmanın "Yönetici"
-- rolü bütün izinleri alıyordu. Yedekleme motoru ise firma değil, kurulum
-- seviyesinde çalışır: bütün veritabanını yedekler ve geri yükler. Aynı
-- kurulumda birden fazla firma varsa, herhangi birinin yöneticisi diğer
-- firmaların verisini indirebiliyor ya da hepsini başka bir yedekle
-- değiştirebiliyordu.
--
-- Çözüm: kurulum seviyesinde ayrı bir operatör listesi. Firma rolü bu yetkiyi
-- artık miras vermez.

CREATE TABLE IF NOT EXISTS installation_operators (
    user_id    uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    granted_at timestamptz NOT NULL DEFAULT now(),
    granted_by uuid REFERENCES users(id) ON DELETE SET NULL,
    note       text NOT NULL DEFAULT ''
);

COMMENT ON TABLE installation_operators IS
  'Kurulumun tamamı üzerinde yedek alma/geri yükleme yetkisi olan kullanıcılar. '
  'Firma rolünden bağımsızdır ve firma rolleriyle miras alınmaz.';

-- Mevcut meşru erişimi taşı.
--
-- Kurulumu tamamlayan kullanıcı bu kurulumun sahibidir; erişimi kesilmemelidir.
-- Bu, "bütün firma yöneticileri" kümesinden daha dardır — kasıtlı olarak: bu
-- değişikliğin amacı tam da o kümeyi daraltmaktır. Yanlışlıkla erişimini
-- kaybeden bir operatör, sunucuda `varyaone system operator add <e-posta>` ile
-- geri kazanır; sunucuya erişim, firma yöneticisi olmaktan daha yüksek bir
-- eşiktir ve bu yetkinin doğru eşiği odur.
INSERT INTO installation_operators (user_id, note)
SELECT completed_by, 'kurulumu tamamlayan kullanıcı (000159 ile taşındı)'
FROM instance_setup
WHERE completed_by IS NOT NULL
ON CONFLICT (user_id) DO NOTHING;

-- Hiç kurulum kaydı yoksa (çok eski kurulum), tek firmalı bir kurulumda o
-- firmanın yöneticilerini taşı. Birden fazla firma varsa kimse otomatik
-- taşınmaz: hangisinin kurulum sahibi olduğunu tahmin etmek, bu bulgunun
-- kendisini tekrar üretmek olurdu.
INSERT INTO installation_operators (user_id, note)
SELECT DISTINCT mr.user_id, 'tek firmalı kurulumun yöneticisi (000159 ile taşındı)'
FROM membership_roles mr
JOIN role_permissions rp ON rp.company_id = mr.company_id AND rp.role_id = mr.role_id
WHERE rp.permission_code = 'system.backup.manage'
  AND (SELECT count(*) FROM companies WHERE is_active) = 1
  AND NOT EXISTS (SELECT 1 FROM installation_operators)
ON CONFLICT (user_id) DO NOTHING;
