// IranGate panel-wide bilingual UI (English/Persian) — frontend only. I18N_RUNTIME_V8_CLEAN
(() => {
    'use strict';

    const STORAGE_KEY = 'irangate_language';
    const LEGACY_KEY = 'irangate_sidebar_language';

    try {
        if (new URLSearchParams(window.location.search).get('no_i18n') === '1') {
            document.documentElement.lang = 'en';
            document.documentElement.dir = 'ltr';
            document.documentElement.dataset.irangateLang = 'en';
            window.IranGateI18n = Object.freeze({
                disabled: true,
                getLanguage: () => 'en',
                setLanguage: () => 'en',
                toggleLanguage: () => 'en',
                refresh: () => 'en',
                ensureControl: () => null,
                translateTree: () => {},
                t: value => value
            });
            return;
        }
    } catch (_) {}
    const FA = Object.freeze({
        // Global/common
        'Dashboard':'داشبورد','Clients':'کاربران','Analytics':'آمار و تحلیل','Users':'کاربران مدیریتی',
        'Settings':'تنظیمات','API':'API','Logout':'خروج','Refresh':'به‌روزرسانی','Loading':'در حال بارگذاری',
        'Loading...':'در حال بارگذاری...','Loading…':'در حال بارگذاری…','Status':'وضعیت','Status:':'وضعیت:',
        'Actions':'عملیات','Action':'عملیات','Name':'نام','Username':'نام کاربری','Password':'رمز عبور',
        'Email':'ایمیل','Role':'نقش','Created':'تاریخ ایجاد','Expires':'انقضا','Validity':'اعتبار',
        'Traffic':'ترافیک','Used':'مصرف‌شده','Remaining':'باقی‌مانده','Total':'کل','Online':'آنلاین',
        'Offline':'آفلاین','Active':'فعال','Inactive':'غیرفعال','Disabled':'غیرفعال','Enabled':'فعال',
        'Enable':'فعال‌کردن','Disable':'غیرفعال‌کردن','Edit':'ویرایش','Delete':'حذف','Remove':'حذف',
        'Save':'ذخیره','Cancel':'انصراف','Create':'ایجاد','Update':'به‌روزرسانی','Clear':'پاک‌کردن',
        'Search':'جستجو','Type':'نوع','Description':'توضیحات','Location':'موقعیت','Connection':'اتصال',
        'Quota':'سهمیه','Access':'دسترسی','Current':'فعلی','Error':'خطا','Warning':'هشدار','Info':'اطلاعات',
        'Debug':'دیباگ','Ready':'آماده','Pending':'در انتظار','View':'مشاهده','Download':'دانلود',
        'Default':'پیش‌فرض','Selected':'انتخاب‌شده','Mode':'حالت','Service':'سرویس','Runtime':'زمان اجرا',
        'Network':'شبکه','Endpoint':'اندپوینت','Interface':'اینترفیس','Subnet':'ساب‌نت','Token':'توکن',
        'Public IP':'IP عمومی','Public Host':'هاست عمومی','Agent':'عامل','Agent URL':'آدرس Agent',
        'Display Name':'نام نمایشی','Location Code':'کد موقعیت','Flag / Emoji':'پرچم / ایموجی',
        'Last Login':'آخرین ورود','Last Run:':'آخرین اجرا:','Last Success:':'آخرین اجرای موفق:',
        'Next Run:':'اجرای بعدی:','Last Error:':'آخرین خطا:','Storage:':'محل ذخیره:',
        'Proxy:':'پروکسی:','Env:':'Env:','Env File:':'فایل Env:','Drop-in:':'Drop-in:',
        'Service:':'سرویس:','No command yet.':'هنوز دستوری اجرا نشده.','No users found.':'کاربری پیدا نشد.',
        'No users yet.':'هنوز کاربری وجود ندارد.','No notifications yet':'هنوز اعلانی وجود ندارد',
        'No nodes found':'نودی پیدا نشد','No selector':'بدون انتخاب‌گر','Never connected':'هرگز متصل نشده',
        'Check Now':'بررسی الآن','Check Status':'بررسی وضعیت','View Details':'مشاهده جزئیات',
        'Save Changes':'ذخیره تغییرات','Copy Subscription Link':'کپی لینک اشتراک',
        'Download Config':'دانلود کانفیگ','Activate':'فعال‌سازی','Deactivate':'غیرفعال‌سازی',
        'Sign In':'ورود','Enter username':'نام کاربری را وارد کنید','Enter password':'رمز عبور را وارد کنید',
        'OpenVPN Management Panel':'پنل مدیریت OpenVPN',

        // Sidebar
        'Main':'اصلی','All Cores Clients':'کاربران همه هسته‌ها','OpenVPN Clients':'کاربران OpenVPN',
        'Admins':'مدیران','Configuration':'پیکربندی','Outbounds':'مسیرهای خروجی','Routing / Outbounds':'مسیریابی / خروجی‌ها',
        'Nodes':'نودها','Server Checker':'بررسی سرور','Resources':'منابع','API Docs':'مستندات API',
        'Usage':'مصرف','Reseller Panel':'پنل نمایندگی','My Panel':'پنل من',

        // Dashboard
        'Checking server':'در حال بررسی سرور','Welcome,':'خوش آمدید،','. Live OpenVPN overview.':'. نمای زنده OpenVPN.',
        'Manage Clients':'مدیریت کاربران','Total Users':'کل کاربران','All records':'همه رکوردها',
        'Every OpenVPN client record.':'تمام رکوردهای کاربران OpenVPN.','Active Users':'کاربران فعال',
        'Usable':'قابل استفاده','Has remaining traffic and valid time.':'دارای ترافیک و زمان معتبر باقی‌مانده.',
        'Online Now':'آنلاین الآن','Live':'زنده','Connected to OpenVPN right now.':'در حال حاضر به OpenVPN متصل است.',
        'Needs Attention':'نیازمند بررسی','Review':'بررسی','Expired, depleted, suspended or missing quota.':'منقضی، تمام‌شده، تعلیق‌شده یا بدون سهمیه.',
        'Orphaned certificates detected':'گواهی‌های بدون رکورد شناسایی شدند','Cleanup':'پاک‌سازی',
        'Usage Overview':'نمای کلی مصرف','Real values calculated from the current client dataset.':'مقادیر واقعی محاسبه‌شده از داده‌های فعلی کاربران.',
        'Total Traffic Used':'کل ترافیک مصرف‌شده','Active Rate':'نرخ فعال','Online Rate':'نرخ آنلاین',
        'Server Health':'سلامت سرور','Backend-reported live metrics.':'شاخص‌های زنده گزارش‌شده توسط Backend.',
        'CPU usage':'مصرف CPU','Memory used':'حافظه مصرف‌شده','Uptime':'زمان فعالیت',
        'Start':'شروع','Restart':'راه‌اندازی مجدد','Stop':'توقف','Recent Users':'کاربران اخیر',
        'Quota validity and live connection are intentionally shown separately.':'اعتبار سهمیه و اتصال زنده جداگانه نمایش داده می‌شوند.',
        'View all users →':'مشاهده همه کاربران ←',

        // Delegated dashboard / usage
        '. This dashboard shows only your own panel and users.':'. این داشبورد فقط پنل و کاربران خود شما را نشان می‌دهد.',
        'All users created by your account.':'همه کاربرانی که توسط حساب شما ساخته شده‌اند.',
        'Users with remaining traffic and valid time.':'کاربران دارای ترافیک و زمان معتبر باقی‌مانده.',
        'Your users connected right now.':'کاربران شما که اکنون متصل هستند.',
        'Expired or depleted users.':'کاربران منقضی یا تمام‌شده.',
        'Panel Traffic':'ترافیک پنل','Your delegated traffic pool.':'سهمیه ترافیک پنل نمایندگی شما.',
        'Usage details →':'جزئیات مصرف ←','Traffic usage':'مصرف ترافیک','Panel Validity':'اعتبار پنل',
        'Time remaining on your reseller account.':'زمان باقی‌مانده حساب نمایندگی شما.',
        'days remaining':'روز باقی‌مانده','Expires at':'تاریخ انقضا',
        'Status is based on both traffic and expiry.':'وضعیت بر اساس ترافیک و تاریخ انقضا تعیین می‌شود.',
        'Traffic, validity and user usage for your own panel.':'ترافیک، اعتبار و مصرف کاربران پنل شما.',
        'Total Traffic':'کل ترافیک','Days Left':'روز باقی‌مانده','Traffic Pool':'سهمیه ترافیک',
        'Used versus remaining delegated traffic.':'مقایسه ترافیک مصرف‌شده و باقی‌مانده نمایندگی.',
        'Top User Usage':'بیشترین مصرف کاربران','Current cumulative usage of your highest-traffic users.':'مصرف تجمعی فعلی کاربران پرمصرف.',
        'Panel Usage':'مصرف پنل','Live percentage of your total traffic allocation.':'درصد زنده از کل سهمیه ترافیک.',
        'User Health':'وضعیت کاربران','User Usage Details':'جزئیات مصرف کاربران',
        'Only users owned by your delegated account are shown.':'فقط کاربران متعلق به حساب نمایندگی شما نمایش داده می‌شوند.',
        'Used / Limit':'مصرف / سقف',

        // Clients
        'Client Management':'مدیریت کاربران','Manage access, quota, validity and client configs from one workspace.':'مدیریت دسترسی، سهمیه، اعتبار و کانفیگ کاربران در یک صفحه.',
        'Workspace':'فضای کاری','Add Client':'افزودن کاربر','ALL':'همه','Total clients':'کل کاربران',
        'ON':'روشن','Online now':'آنلاین الآن','OK':'اوکی','Ready / offline':'آماده / آفلاین',
        'Ended / blocked':'پایان‌یافته / مسدود','All Status':'همه وضعیت‌ها','Ready / Offline':'آماده / آفلاین',
        'Ended / Blocked':'پایان‌یافته / مسدود','Plan / Remaining':'پلن / باقی‌مانده',
        'Loading clients...':'در حال بارگذاری کاربران...','Orphaned Certificates':'گواهی‌های بدون رکورد',
        'Certificates that exist in PKI but not in database -':'گواهی‌هایی که در PKI هستند اما در دیتابیس وجود ندارند -',
        'SECURITY RISK!':'خطر امنیتی!','Add New Client':'افزودن کاربر جدید','Client Name':'نام کاربر',
        'Use letters, numbers, underscore, and hyphen only':'فقط حروف، اعداد، زیرخط و خط تیره مجاز است',
        'Leave as 0 for unlimited traffic':'برای ترافیک نامحدود مقدار 0 بگذارید','Connection Limit':'محدودیت اتصال',
        'Server Configuration':'پیکربندی سرور','Select Inbound':'انتخاب Inbound','Port:':'پورت:','Protocol:':'پروتکل:','Cipher:':'رمزنگاری:',
        'Node / Location Selection':'انتخاب نود / موقعیت','Germany Master':'مستر آلمان','Loading nodes...':'در حال بارگذاری نودها...',
        'Selected nodes will also generate .ovpn configs and appear in subscription downloads.':'برای نودهای انتخاب‌شده نیز کانفیگ .ovpn ساخته و در دانلودهای اشتراک نمایش داده می‌شود.',
        'Create Client':'ساخت کاربر','Client Details':'جزئیات کاربر','No clients found. Click "Add Client" to create one.':'کاربری پیدا نشد. برای ساخت کاربر روی «افزودن کاربر» بزنید.',
        'Rework':'صدور مجدد','Sub':'اشتراک','No remote nodes enabled':'هیچ نود راه‌دوری فعال نیست',
        'Failed to load nodes':'بارگذاری نودها ناموفق بود','Error loading configurations':'خطا در بارگذاری کانفیگ‌ها',
        'Master / Local node':'مستر / نود محلی','IP Address':'آدرس IP','Data Uploaded':'داده آپلودشده',
        'Data Downloaded':'داده دانلودشده','Edit Client':'ویرایش کاربر','Expiration Days From Now':'تعداد روز اعتبار از امروز',
        'Traffic Limit':'سقف ترافیک','Available Nodes':'نودهای موجود',
        "Untick a node to remove it from this client's subscription and access list.":'برای حذف نود از اشتراک و فهرست دسترسی کاربر، تیک آن را بردارید.',
        'Active / allow this client to connect':'فعال / اجازه اتصال به این کاربر',
        'Loading locations...':'در حال بارگذاری موقعیت‌ها...','Remote Nodes':'نودهای راه‌دور',
        'OpenVPN Port Locations':'موقعیت‌های پورت OpenVPN','No remote nodes or OpenVPN port locations enabled':'هیچ نود راه‌دور یا موقعیت پورت OpenVPN فعالی وجود ندارد',
        'Failed to load locations':'بارگذاری موقعیت‌ها ناموفق بود','Client summary':'خلاصه کاربر',
        'Search clients...':'جستجوی کاربران...','Revoke old credentials and generate new ones':'لغو اطلاعات قبلی و ساخت اطلاعات جدید',
        '0 = Unlimited':'0 = نامحدود','Disable this node':'غیرفعال‌کردن این نود','Enable this node':'فعال‌کردن این نود',

        // Admins
        'Reseller Admins':'مدیران نمایندگی','Manage reseller accounts, quotas, validity and owned users from one fast workspace.':'مدیریت حساب‌های نمایندگی، سهمیه، اعتبار و کاربران در یک فضای کاری.',
        '+ Add Admin':'+ افزودن مدیر','Total Admins':'کل مدیران','Active Admins':'مدیران فعال',
        'Managed Users':'کاربران مدیریت‌شده','Traffic Pools':'سهمیه‌های ترافیک','Used / total assigned':'مصرف / کل تخصیص‌یافته',
        'Admin List':'فهرست مدیران','Loading accounts…':'در حال بارگذاری حساب‌ها…','Admin':'مدیر',
        'Traffic Used / Total':'مصرف ترافیک / کل','Create Delegated Admin':'ساخت مدیر نمایندگی',
        'This admin can only manage its own OpenVPN users.':'این مدیر فقط می‌تواند کاربران OpenVPN متعلق به خود را مدیریت کند.',
        'Traffic quota':'سهمیه ترافیک','Disabling an admin, exhausting its traffic pool, or expiring its time blocks all owned users on Master, Nodes and IKEv2.':'غیرفعال شدن مدیر، اتمام ترافیک یا پایان اعتبار او، تمام کاربرانش را روی Master، Nodes و IKEv2 مسدود می‌کند.',
        'Create Admin':'ساخت مدیر','Admin Details':'جزئیات مدیر','Owned-user status and delegated traffic pool.':'وضعیت کاربران تحت مدیریت و سهمیه ترافیک نمایندگی.',
        'No matching delegated admins.':'مدیر نمایندگی مطابق پیدا نشد.','owned users':'کاربر تحت مدیریت',
        '+ Traffic':'+ ترافیک','+ Time':'+ زمان','Loading client details…':'در حال بارگذاری جزئیات کاربران…',
        'No owned users found.':'کاربر تحت مدیریتی پیدا نشد.','Active users':'کاربران فعال','Days left':'روز باقی‌مانده',
        'Traffic used':'ترافیک مصرف‌شده','Total traffic':'کل ترافیک','Search admin...':'جستجوی مدیر...',
        'Minimum 8 characters':'حداقل ۸ کاراکتر',

        // Nodes
        'Nodes / Locations':'نودها / موقعیت‌ها','Manage remote OpenVPN nodes and local UDP port locations connected to this panel':'مدیریت نودهای راه‌دور OpenVPN و موقعیت‌های پورت UDP محلی متصل به این پنل',
        'Add New Node':'افزودن نود جدید','Node ID':'شناسه نود','Example: turkey, finland, netherlands':'مثال: turkey, finland, netherlands',
        'Example: DE, TR, FI, NL, US':'مثال: DE, TR, FI, NL, US','The node-agent API address':'آدرس API نود Agent',
        'Add Node':'افزودن نود','Remote Nodes / Master':'نودهای راه‌دور / مستر','ID':'شناسه',
        'Local OpenVPN Port Locations':'موقعیت‌های پورت محلی OpenVPN',
        'Create independent local OpenVPN UDP ports with separate interfaces, subnets and outbound routing.':'ساخت پورت‌های UDP مستقل OpenVPN با اینترفیس، ساب‌نت و مسیریابی خروجی جداگانه.',
        'Refresh Locations':'به‌روزرسانی موقعیت‌ها','Apply All Locations':'اعمال همه موقعیت‌ها',
        'Add Local Port Location':'افزودن موقعیت پورت محلی','Location ID':'شناسه موقعیت',
        'Lowercase letters, numbers and hyphens.':'حروف کوچک انگلیسی، اعداد و خط تیره.',
        'Leave empty to use the main OpenVPN public host.':'برای استفاده از هاست عمومی اصلی OpenVPN خالی بگذارید.',
        'OpenVPN UDP Port':'پورت UDP اوپن‌وی‌پی‌ان','UDP 500 and 4500 are reserved for IKEv2.':'پورت‌های UDP 500 و 4500 برای IKEv2 رزرو هستند.',
        'Interface, /24 subnet, Management port, TPROXY port and Status file are allocated automatically to prevent conflicts.':'اینترفیس، ساب‌نت /24، پورت مدیریت، پورت TPROXY و فایل وضعیت برای جلوگیری از تداخل خودکار تخصیص می‌یابند.',
        'Add Port Location':'افزودن موقعیت پورت','Configured Local Port Locations':'موقعیت‌های پورت محلی پیکربندی‌شده',
        'Runtime status is based on the generated systemd OpenVPN service.':'وضعیت اجرا بر اساس سرویس systemd ساخته‌شده برای OpenVPN است.',
        'ID / Name':'شناسه / نام','Edit Node':'ویرایش نود','Save and Apply':'ذخیره و اعمال',
        'Edit Local OpenVPN Port Location':'ویرایش موقعیت پورت محلی OpenVPN','Automatically Allocated Runtime Values':'مقادیر اجرایی تخصیص‌یافته خودکار',
        'Management Port':'پورت مدیریت','TPROXY Port':'پورت TPROXY','Status File':'فایل وضعیت',
        'No local OpenVPN port locations configured.':'هیچ موقعیت پورت محلی OpenVPN پیکربندی نشده است.',
        'Management:':'مدیریت:','Local master stays enabled':'مستر محلی همیشه فعال می‌ماند',

        // Analytics
        'Live operational snapshot of client access, cumulative traffic and account lifecycle. No demo or randomly generated metrics are used.':'نمای زنده عملیاتی از دسترسی کاربران، ترافیک تجمعی و چرخه عمر حساب؛ هیچ داده نمایشی یا تصادفی استفاده نمی‌شود.',
        'Live panel data':'داده زنده پنل','Creation: 24 Hours':'ایجاد: ۲۴ ساعت','Creation: 7 Days':'ایجاد: ۷ روز',
        'Creation: 30 Days':'ایجاد: ۳۰ روز','Creation: 90 Days':'ایجاد: ۹۰ روز','Creation: All Time':'ایجاد: کل زمان',
        'Export CSV':'خروجی CSV','Cumulative Traffic':'ترافیک تجمعی','Current persisted usage across visible clients':'مصرف ثبت‌شده فعلی کاربران قابل مشاهده',
        'Clients with a current connection/session signal':'کاربران دارای اتصال/نشست فعال','Access Ready':'دسترسی آماده',
        'Enabled accounts with quota and validity remaining':'حساب‌های فعال دارای سهمیه و اعتبار باقی‌مانده',
        'Expired, quota exhausted or disabled access':'منقضی، سهمیه تمام‌شده یا دسترسی غیرفعال',
        'Top Client Traffic':'بیشترین ترافیک کاربران','Top 10':'۱۰ کاربر برتر','Client Creation Timeline':'خط زمانی ساخت کاربران',
        'Real client creation timestamps for the selected window.':'زمان واقعی ساخت کاربران در بازه انتخابی.',
        'Traffic Direction':'جهت ترافیک','Upload/download split derived from current persisted counters.':'تفکیک آپلود/دانلود بر اساس شمارنده‌های ثبت‌شده فعلی.',
        'Snapshot':'نمای لحظه‌ای','Access Distribution':'توزیع دسترسی','Online, ready, disabled and ended client states.':'وضعیت کاربران آنلاین، آماده، غیرفعال و پایان‌یافته.',
        'Top Clients by Traffic':'کاربران برتر بر اساس ترافیک','Quota position and cumulative traffic for the busiest visible clients.':'وضعیت سهمیه و ترافیک تجمعی پرمصرف‌ترین کاربران.',
        'Loading analytics…':'در حال بارگذاری آمار…','Rank':'رتبه','Limit':'سقف','Client Access':'دسترسی کاربران',
        'Total visible clients':'کل کاربران قابل مشاهده','Enabled access':'دسترسی فعال','Manually disabled':'غیرفعال‌شده دستی',
        'Quota Health':'سلامت سهمیه','Expired by time':'منقضی‌شده زمانی','Traffic exhausted':'ترافیک تمام‌شده',
        '≥ 85% traffic used':'≥ ۸۵٪ ترافیک مصرف‌شده','Data Source':'منبع داده','Clients API':'API کاربران',
        'Stats API':'API آمار','Auto refresh':'به‌روزرسانی خودکار','60 sec':'۶۰ ثانیه',

        // Settings
        'Manage panel security, server identity, appearance, Telegram automation, backups and proxy/server behavior from one control center.':'مدیریت امنیت پنل، هویت سرور، ظاهر، خودکارسازی تلگرام، بکاپ و تنظیمات پروکسی/سرور از یک مرکز کنترل.',
        'Root control center':'مرکز کنترل Root','Admin Settings':'تنظیمات مدیر','Server Settings':'تنظیمات سرور',
        'Theme':'پوسته','Telegram Bot':'ربات تلگرام','Backup & Restore':'پشتیبان‌گیری و بازیابی','System Info':'اطلاعات سیستم',
        'Proxy & Server':'پروکسی و سرور','Current Password':'رمز عبور فعلی','New Password':'رمز عبور جدید',
        'Confirm New Password':'تکرار رمز عبور جدید','Change Password':'تغییر رمز عبور','Server IP / Domain':'IP / دامنه سرور',
        "This will be used as the 'remote' parameter in all client configs.":'این مقدار به‌عنوان پارامتر remote در همه کانفیگ‌های کاربران استفاده می‌شود.',
        'Save Server Settings':'ذخیره تنظیمات سرور','Webpanel Theme':'پوسته پنل','Choose from our collection of beautiful themes or customize your own':'از پوسته‌های آماده انتخاب کنید یا پوسته دلخواه بسازید',
        '🌐 Get Online Themes':'🌐 دریافت پوسته‌های آنلاین','Download additional themes from our community repository':'دانلود پوسته‌های بیشتر از مخزن جامعه',
        'Default theme with navy blue and cyan accents':'پوسته پیش‌فرض با سرمه‌ای و فیروزه‌ای','Modern black and white minimalist design':'طراحی مینیمال مدرن سیاه و سفید',
        'Bold red, orange, and black color scheme':'ترکیب رنگ قرمز، نارنجی و مشکی','Calming blue and teal ocean-inspired theme':'پوسته آرام آبی و فیروزه‌ای الهام‌گرفته از اقیانوس',
        'Natural green and earth tone theme':'پوسته طبیعی سبز و رنگ‌های خاکی','Custom Theme':'پوسته سفارشی',
        'Create your own theme with custom CSS and JavaScript':'ساخت پوسته دلخواه با CSS و JavaScript','Custom CSS':'CSS سفارشی',
        'Add custom styles to override default theme':'افزودن استایل برای بازنویسی پوسته پیش‌فرض','Custom JavaScript':'JavaScript سفارشی',
        'Save Custom Theme':'ذخیره پوسته سفارشی','Reset to Default':'بازنشانی به پیش‌فرض','Enable Telegram Notifications':'فعال‌سازی اعلان‌های تلگرام',
        'Save Telegram Settings':'ذخیره تنظیمات تلگرام','☁️ Automatic Backup to Telegram':'☁️ بکاپ خودکار به تلگرام',
        'Creates a complete Backup v2, stores it on the server, and sends the same archive using the Bot Token and Chat ID configured above.':'یک Backup v2 کامل می‌سازد، روی سرور ذخیره می‌کند و همان فایل را با Bot Token و Chat ID بالا به تلگرام می‌فرستد.',
        'Enable automatic backup delivery':'فعال‌سازی ارسال خودکار بکاپ','Backup Interval':'فاصله بکاپ','Interval Unit':'واحد فاصله',
        'Minutes':'دقیقه','Hours':'ساعت','Old archives are removed according to retention days. A maximum of 30 copies is kept for this schedule.':'بکاپ‌های قدیمی بر اساس مدت نگهداری حذف می‌شوند؛ حداکثر ۳۰ نسخه برای این برنامه نگهداری می‌شود.',
        '💾 Save Backup Schedule':'💾 ذخیره برنامه بکاپ','📦 Backup & Send Now':'📦 بکاپ و ارسال الآن',
        '📥 Export Backup':'📥 خروجی بکاپ','Download complete Backup v2 with PostgreSQL, users, nodes, usage state, and OpenVPN PKI':'دانلود Backup v2 کامل شامل PostgreSQL، کاربران، نودها، وضعیت مصرف و PKI اوپن‌وی‌پی‌ان',
        'Import Backup':'واردکردن بکاپ','📤 Import Backup':'📤 واردکردن بکاپ','System Information':'اطلاعات سیستم',
        'IranGate Version':'نسخه IranGate','API Status':'وضعیت API','● Online':'● آنلاین','Database Location':'محل دیتابیس',
        'OpenVPN Service':'سرویس OpenVPN','Panel-level proxy for Telegram Bot API, automatic backups, notifications and outbound HTTP/HTTPS requests.':'پروکسی سطح پنل برای API تلگرام، بکاپ خودکار، اعلان‌ها و درخواست‌های خروجی HTTP/HTTPS.',
        'Enable panel proxy':'فعال‌سازی پروکسی پنل','Proxy URL':'آدرس پروکسی','Test URL':'آدرس تست','Test Proxy':'تست پروکسی',
        'Save & Restart Panel':'ذخیره و راه‌اندازی مجدد پنل','Disable Proxy':'غیرفعال‌سازی پروکسی','Raw Result':'خروجی خام',
        'IKEv2 Server IP / Domain':'IP / دامنه سرور IKEv2',
        'This address is used only for IKEv2 / IPSec Server and Identifier. Leave empty to use the OpenVPN server address.':'این آدرس فقط برای Server و Identifier در IKEv2/IPSec استفاده می‌شود؛ برای استفاده از آدرس OpenVPN خالی بگذارید.',

        // Routing
        'Route primary OpenVPN/IKEv2 traffic and each OpenVPN port location through independent outbound strategies.':'مسیریابی ترافیک اصلی OpenVPN/IKEv2 و هر موقعیت پورت OpenVPN از طریق خروجی‌های مستقل.',
        'Bypass / GOST Mode':'حالت Bypass / GOST','Outbound Form':'فرم خروجی','Tag':'تگ','Auto / xray parser':'خودکار / پردازشگر Xray',
        'Share Link':'لینک اشتراک','Add New':'افزودن جدید','Update Selected':'به‌روزرسانی انتخاب‌شده','Route VPN Client Traffic':'مسیریابی ترافیک کاربران VPN',
        'Inbound':'ورودی','OpenVPN + IKEv2 — Shared Outbound':'OpenVPN + IKEv2 — خروجی مشترک','OpenVPN traffic':'ترافیک OpenVPN',
        'Balancer Strategy':'استراتژی بالانسر','Least Ping / URLTest':'کمترین پینگ / URLTest','Manual Selector':'انتخاب‌گر دستی',
        'Failover / Priority Backup':'Failover / بکاپ اولویت‌دار','Random — Xray Engine Required':'تصادفی — نیازمند موتور Xray',
        'Round Robin — Xray Engine Required':'Round Robin — نیازمند موتور Xray','Least Load — Xray Engine Required':'کمترین بار — نیازمند موتور Xray',
        'Selected Inbound Route Preview':'پیش‌نمایش مسیر ورودی انتخاب‌شده','Ping Checked':'پینگ بررسی شد','Bind Checked':'Bind بررسی شد',
        'Activate Checked':'فعال‌سازی بررسی شد','Edit Checked':'ویرایش بررسی شد','Delete Checked':'حذف بررسی شد',
        'Configured Outbounds':'خروجی‌های پیکربندی‌شده','Tick outbounds for single/failover routing.':'برای مسیریابی single/failover خروجی‌ها را تیک بزنید.',
        'Use':'استفاده','Link / Remote':'لینک / Remote','Routing Overview':'نمای کلی مسیریابی','Raw CLI Output':'خروجی خام CLI',
        'Last Action':'آخرین عملیات','Raw details are available below.':'جزئیات خام در پایین موجود است.','Ping Result':'نتیجه پینگ',
        'Avg':'میانگین','Best':'بهترین','Worst':'بدترین','No outbounds configured.':'هیچ خروجی پیکربندی نشده است.',
        'Mode/Strategy':'حالت / استراتژی','Selector':'انتخاب‌گر','Direct / no selector configured':'Direct / بدون انتخاب‌گر',

        // Maintenance
        'Maintenance':'نگهداری','Backup, logs, and system maintenance tools':'ابزارهای بکاپ، لاگ و نگهداری سیستم',
        'Log Viewer':'مشاهده لاگ','System Cleanup':'پاک‌سازی سیستم','Backup Schedules':'برنامه‌های بکاپ',
        'Add Schedule':'افزودن برنامه','Available Backups':'بکاپ‌های موجود','Filename':'نام فایل','Size':'حجم',
        'OpenVPN':'OpenVPN','WebPanel':'وب‌پنل','System':'سیستم','Level':'سطح','All Levels':'همه سطوح',
        'Lines':'خطوط','Last 50 lines':'۵۰ خط آخر','Last 100 lines':'۱۰۰ خط آخر','Last 500 lines':'۵۰۰ خط آخر',
        'Last 1000 lines':'۱۰۰۰ خط آخر','Tail Logs':'نمایش انتهای لاگ','Stream':'پخش زنده','Auto-refresh:':'به‌روزرسانی خودکار:',
        'Log Output':'خروجی لاگ','Select log type and click "Tail Logs" to view recent entries...':'نوع لاگ را انتخاب کنید و برای دیدن موارد اخیر روی «نمایش انتهای لاگ» بزنید...',
        'Disk Usage':'مصرف دیسک','Log Cleanup':'پاک‌سازی لاگ','Remove old log files to free up disk space':'حذف لاگ‌های قدیمی برای آزادسازی فضای دیسک',
        'Clean logs older than:':'حذف لاگ‌های قدیمی‌تر از:','7 days':'۷ روز','30 days':'۳۰ روز','90 days':'۹۰ روز',
        'Clean Logs':'پاک‌سازی لاگ‌ها','History Cleanup':'پاک‌سازی تاریخچه','Remove old connection history and statistics':'حذف تاریخچه اتصال و آمار قدیمی',
        'Clean history older than:':'حذف تاریخچه قدیمی‌تر از:','180 days':'۱۸۰ روز','Clean History':'پاک‌سازی تاریخچه',
        'Database Optimization':'بهینه‌سازی دیتابیس','Optimize database files and remove unused data':'بهینه‌سازی فایل‌های دیتابیس و حذف داده‌های بدون استفاده',
        'Optimize Database':'بهینه‌سازی دیتابیس','Remove orphaned certificates that can still connect':'حذف گواهی‌های بدون رکورد که هنوز امکان اتصال دارند',
        'Clean Orphaned Certs':'پاک‌سازی گواهی‌های بدون رکورد','Add Backup Schedule':'افزودن برنامه بکاپ',
        'Cron Expression':'عبارت Cron','Format: minute hour day month weekday':'فرمت: دقیقه ساعت روز ماه روزهفته',
        'Backup Path':'مسیر بکاپ','Enable this schedule':'فعال‌سازی این برنامه',

        // Notifications
        'Notifications':'اعلان‌ها','Configure Telegram and email notifications':'پیکربندی اعلان‌های تلگرام و ایمیل',
        'Telegram Bot Configuration':'پیکربندی ربات تلگرام','Checking...':'در حال بررسی...','Bot Token':'توکن ربات','Chat ID':'شناسه چت',
        'Enable Telegram notifications':'فعال‌سازی اعلان‌های تلگرام','Save Configuration':'ذخیره پیکربندی','Test Connection':'تست اتصال',
        'Notification Preferences':'تنظیمات اعلان','Client Events':'رویدادهای کاربران','Client expiring soon':'نزدیک بودن انقضای کاربر',
        'Client quota exceeded':'اتمام سهمیه کاربر','New client created':'ساخت کاربر جدید','System Events':'رویدادهای سیستم',
        'Server resource warnings':'هشدار منابع سرور','OpenVPN service down':'قطع سرویس OpenVPN','OpenVPN service restarted':'راه‌اندازی مجدد سرویس OpenVPN',
        'Notify when client expires in:':'اعلان پیش از انقضای کاربر:','1 day':'۱ روز','3 days':'۳ روز','14 days':'۱۴ روز',
        'Save Settings':'ذخیره تنظیمات','Auto-Actions':'عملیات خودکار','Add Auto-Action':'افزودن عملیات خودکار',
        'Recent Notifications':'اعلان‌های اخیر','Time':'زمان','Message':'پیام','Add New User':'افزودن کاربر جدید',
        'Trigger':'تریگر','Select trigger':'انتخاب تریگر','Client Expired':'کاربر منقضی شد','Client Expiring':'کاربر در حال انقضا',
        'Quota Exceeded':'سهمیه تمام شد','Resource Warning':'هشدار منابع','Service Down':'سرویس قطع شد',
        'Select action':'انتخاب عملیات','Disable Client':'غیرفعال‌کردن کاربر','Extend Client':'تمدید کاربر',
        'Restart Service':'راه‌اندازی مجدد سرویس','Send Notification':'ارسال اعلان','Enable this auto-action':'فعال‌سازی این عملیات خودکار',

        // Proxy
        'Proxy and Server':'پروکسی و سرور','Configure panel-level proxy for Telegram Bot API, backup delivery, and outbound HTTP/HTTPS requests.':'پیکربندی پروکسی سطح پنل برای API تلگرام، ارسال بکاپ و درخواست‌های خروجی HTTP/HTTPS.',
        'SOCKS / HTTP Proxy':'پروکسی SOCKS / HTTP','This affects the webpanel service only. It does not route OpenVPN users. For Telegram in Iran, use a reachable SOCKS5 or HTTP proxy.':'این تنظیم فقط روی سرویس وب‌پنل اثر دارد و ترافیک کاربران OpenVPN را مسیریابی نمی‌کند. برای تلگرام از پروکسی SOCKS5 یا HTTP در دسترس استفاده کنید.',
        'Supported: socks5://user:pass@host:port or http://user:pass@host:port':'پشتیبانی: socks5://user:pass@host:port یا http://user:pass@host:port',
        'For bot test you can use: https://api.telegram.org/botTOKEN/getMe':'برای تست ربات می‌توانید از https://api.telegram.org/botTOKEN/getMe استفاده کنید',
        'Last Result':'آخرین نتیجه',

        // Server checker
        'Monitor your server connectivity from multiple Iranian locations':'بررسی اتصال سرور از چند موقعیت داخل ایران',
        'Check Mode':'حالت بررسی','Current Server':'سرور فعلی','Enter Custom IP':'واردکردن IP دلخواه',
        'Start Ping Check':'شروع تست پینگ','Active Check':'بررسی فعال','Request ID':'شناسه درخواست',
        'Host':'هاست','View Full Report on check-host.net':'مشاهده گزارش کامل در check-host.net',
        'Node':'نود','RTT':'RTT','Responder':'پاسخ‌دهنده','Recent Checks':'بررسی‌های اخیر','Timestamp':'زمان',
        'Avg RTT':'میانگین RTT','No checks performed yet':'هنوز بررسی‌ای انجام نشده','Loading results...':'در حال بارگذاری نتایج...',
        'Enter IP address (e.g., 8.8.8.8)':'آدرس IP را وارد کنید (مثال: 8.8.8.8)',

        // API docs / AI
        'API Documentation':'مستندات API','Interactive REST API reference for IranGate':'مرجع تعاملی REST API برای IranGate',
        'Authentication':'احراز هویت','Login and receive JWT token':'ورود و دریافت توکن JWT','Request:':'درخواست:','Response:':'پاسخ:',
        'Client Management':'مدیریت کاربران','List all VPN clients':'فهرست همه کاربران VPN','Create new client':'ساخت کاربر جدید',
        'Download .ovpn configuration file':'دانلود فایل کانفیگ .ovpn','Revoke all orphaned certificates':'لغو همه گواهی‌های بدون رکورد',
        'Other Endpoints':'سایر Endpointها','Code Examples':'نمونه کدها','cURL Example:':'نمونه cURL:','JavaScript Example:':'نمونه JavaScript:',
        'AI Core Management':'مدیریت هسته هوش مصنوعی','Intelligent automation and management features':'قابلیت‌های مدیریت و خودکارسازی هوشمند',
        'AI Core Features Coming Soon':'قابلیت‌های AI Core به‌زودی','Advanced AI-powered features for intelligent client management, traffic analysis, and automated optimization will be available here.':'قابلیت‌های پیشرفته هوش مصنوعی برای مدیریت کاربران، تحلیل ترافیک و بهینه‌سازی خودکار در این بخش ارائه می‌شود.',
        '🤖 Auto-Scaling':'🤖 مقیاس‌پذیری خودکار','Automatic resource allocation based on traffic patterns':'تخصیص خودکار منابع بر اساس الگوی ترافیک',
        '📊 Predictive Analytics':'📊 تحلیل پیش‌بینانه','Forecast client usage and bandwidth requirements':'پیش‌بینی مصرف کاربران و نیاز پهنای باند',
        '🛡️ Threat Detection':'🛡️ تشخیص تهدید','AI-powered security monitoring and alerts':'پایش امنیتی و هشدار با هوش مصنوعی',
        '⚡ Load Balancing':'⚡ توزیع بار','Intelligent traffic distribution across servers':'توزیع هوشمند ترافیک بین سرورها',
        '🔍 Anomaly Detection':'🔍 تشخیص ناهنجاری','Identify unusual patterns and potential issues':'شناسایی الگوهای غیرعادی و مشکلات احتمالی',
        '📈 Optimization':'📈 بهینه‌سازی','Automated performance tuning and recommendations':'تنظیم خودکار عملکرد و پیشنهادها',

        // User management
        'User Management':'مدیریت کاربران مدیریتی','Manage admin users and roles':'مدیریت کاربران مدیر و نقش‌ها',
        'Add User':'افزودن کاربر','Add New User':'افزودن کاربر جدید','Admin - Full Access':'مدیر - دسترسی کامل',
        'Moderator - Manage Clients':'ناظر - مدیریت کاربران','Viewer - Read Only':'مشاهده‌گر - فقط خواندنی',

        // Remaining shared/static labels found by the full-page translation audit
        'User':'کاربر','Client':'کاربر','Cipher':'رمزنگاری','Ping':'پینگ','Port':'پورت',
        'loading...':'در حال بارگذاری...','REMAINING':'باقی‌مانده','single':'تکی','failover':'Failover',
        'Selected Inbound':'ورودی انتخاب‌شده','Mode / Strategy':'حالت / استراتژی','Active Route':'مسیر فعال',
        'Outbound selector for this inbound':'انتخاب‌گر خروجی برای این ورودی',
        'No client data available.':'داده‌ای از کاربران موجود نیست.',
        'Untick a node to remove it from this client\'s subscription and access list.':'برای حذف نود از اشتراک و فهرست دسترسی این کاربر، تیک آن را بردارید.',
        'Revoke old Master/Node credentials, rotate IKEv2 password, and issue a new subscription token':'اطلاعات قبلی Master/Node را لغو، رمز IKEv2 را تغییر و توکن اشتراک جدید صادر می‌کند',
        '- Get OpenVPN config':'- دریافت کانفیگ OpenVPN','- Server statistics':'- آمار سرور',
        '- Server status':'- وضعیت سرور','- Restart OpenVPN':'- راه‌اندازی مجدد OpenVPN',
        '- List admin users':'- فهرست مدیران','- Create admin user':'- ساخت مدیر',
        '- List client groups':'- فهرست گروه‌های کاربران','- Create group':'- ساخت گروه',
        '- Export backup':'- خروجی بکاپ','- Import backup':'- واردکردن بکاپ',
        'Like Sanaei: Least Ping is active with sing-box URLTest. Random/RoundRobin/LeastLoad need Xray engine for exact behavior.':'مانند ثنایی: Least Ping با URLTest در sing-box فعال است. حالت‌های Random/RoundRobin/LeastLoad برای رفتار دقیق به موتور Xray نیاز دارند.',
        '💫 Coming Soon':'💫 به‌زودی'
    });

    const titleFA = Object.freeze({
        'Dashboard - IranGate':'داشبورد - IranGate',
        'Clients - NET SPOOF':'کاربران - NET SPOOF',
        'Analytics - NET SPOOF':'آمار و تحلیل - NET SPOOF',
        'Admins - NET SPOOF':'مدیران - NET SPOOF',
        'Nodes - NET SPOOF':'نودها - NET SPOOF',
        'Settings - NET SPOOF':'تنظیمات - NET SPOOF',
        'Outbounds - NET SPOOF':'مسیرهای خروجی - NET SPOOF',
        'Usage - IranGate':'مصرف - IranGate',
        'Maintenance - IranGate':'نگهداری - IranGate',
        'Notifications - IranGate':'اعلان‌ها - IranGate',
        'Server Checker - IranGate':'بررسی سرور - IranGate',
        'API Documentation - IranGate':'مستندات API - IranGate',
        'User Management - IranGate':'مدیریت کاربران - IranGate',
        'IranGate - Proxy and Server':'IranGate - پروکسی و سرور',
        'AI Core - IranGate':'AI Core - IranGate',
        'NET SPOOF - Login':'NET SPOOF - ورود'
    });


    const EXTRA_FA = Object.freeze({
        'Creation timeline window':'بازه خط زمانی ایجاد',
        'Analytics summary':'خلاصه آمار',
        'Tabs':'تب‌ها',
        'Search logs...':'جستجوی لاگ‌ها...',
        '0 2 * * * (daily at 2 AM)':'0 2 * * * (روزانه ساعت ۲ بامداد)',
        "Reset used traffic to 0 - فقط اگر می‌خواهی مصرف قبلی پاک شود تیک بزن":'بازنشانی ترافیک مصرف‌شده به ۰ - فقط اگر می‌خواهید مصرف قبلی پاک شود تیک بزنید',
        'For bot test: https://api.telegram.org/botTOKEN/getMe':'برای تست ربات: https://api.telegram.org/botTOKEN/getMe',
        'Something amazing is coming:':'قابلیت‌های جذابی در راه است:',
        'Automatic tunnel creation based on traffic patterns':'ساخت خودکار تونل بر اساس الگوی ترافیک',
        'Intelligent routing rule generation':'ساخت هوشمند قوانین مسیریابی',
        'Automated client provisioning':'ساخت و آماده‌سازی خودکار کاربران',
        'Dynamic bandwidth allocation':'تخصیص پویای پهنای باند',
        '🎁 Stay tuned for the surprise!':'🎁 منتظر قابلیت‌های جدید باشید!',
        '💫 Wait for the surprise!':'💫 منتظر قابلیت جدید باشید!',
        'Got it':'متوجه شدم'
    });

    const originalText = new WeakMap();
    const renderedText = new WeakMap();
    const originalAttrs = new WeakMap();
    const renderedAttrs = new WeakMap();
    let observer = null;

    function normalizeLang(lang) {
        return lang === 'fa' ? 'fa' : 'en';
    }

    function getLanguage() {
        return normalizeLang(
            localStorage.getItem(STORAGE_KEY) ||
            localStorage.getItem(LEGACY_KEY)
        );
    }

    function rememberLanguage(lang) {
        const normalized = normalizeLang(lang);
        localStorage.setItem(STORAGE_KEY, normalized);
        localStorage.setItem(LEGACY_KEY, normalized);
        return normalized;
    }

    function dynamicTranslate(value) {
        let out = value;
        out = out.replace(/^(\d+)\s+days remaining$/i, '$1 روز باقی‌مانده');
        out = out.replace(/^(\d+)\s+owned users$/i, '$1 کاربر تحت مدیریت');
        out = out.replace(/^(\d+)\s+selected outbounds$/i, '$1 خروجی انتخاب‌شده');
        out = out.replace(/^Loading\s+(.+)\.\.\.$/i, 'در حال بارگذاری $1...');
        out = out.replace(/^(\d+)\s+users?$/i, '$1 کاربر');
        return out;
    }

    function translateValue(value) {
        const raw = String(value);
        const trimmed = raw.trim();
        if (!trimmed) return raw;

        const exact = FA[trimmed] || EXTRA_FA[trimmed];
        const translated = exact || dynamicTranslate(trimmed);
        if (translated === trimmed) return raw;

        const leading = raw.match(/^\s*/)?.[0] || '';
        const trailing = raw.match(/\s*$/)?.[0] || '';
        return leading + translated + trailing;
    }

    function skipTextNode(node) {
        const parent = node.parentElement;
        if (!parent) return true;
        return Boolean(parent.closest(
            'script,style,code,pre,textarea,[data-i18n-skip="true"],#irangate-language-toggle'
        ));
    }

    function processText(node, lang, refreshOriginal = false) {
        if (!node || node.nodeType !== Node.TEXT_NODE || skipTextNode(node)) return;

        if (refreshOriginal) {
            const last = renderedText.get(node);
            if (last !== undefined && node.nodeValue === last) return;
            originalText.set(node, node.nodeValue);
        } else if (!originalText.has(node)) {
            originalText.set(node, node.nodeValue);
        }

        const source = originalText.get(node);
        const target = lang === 'fa' ? translateValue(source) : source;

        if (node.nodeValue !== target) node.nodeValue = target;
        renderedText.set(node, target);
    }

    function attrMaps(element) {
        if (!originalAttrs.has(element)) originalAttrs.set(element, new Map());
        if (!renderedAttrs.has(element)) renderedAttrs.set(element, new Map());
        return [originalAttrs.get(element), renderedAttrs.get(element)];
    }

    function processAttribute(element, attr, lang, refreshOriginal = false) {
        if (!(element instanceof Element) || !element.hasAttribute(attr)) return;
        if (element.closest('[data-i18n-skip="true"],#irangate-language-toggle')) return;

        const [original, rendered] = attrMaps(element);
        const current = element.getAttribute(attr) ?? '';

        if (refreshOriginal) {
            if (rendered.has(attr) && current === rendered.get(attr)) return;
            original.set(attr, current);
        } else if (!original.has(attr)) {
            original.set(attr, current);
        }

        const source = original.get(attr);
        const target = lang === 'fa' ? translateValue(source) : source;
        if (current !== target) element.setAttribute(attr, target);
        rendered.set(attr, target);
    }

    function processElement(element, lang, refreshOriginal = false) {
        if (!(element instanceof Element)) return;

        for (const attr of ['placeholder', 'title', 'aria-label']) {
            processAttribute(element, attr, lang, refreshOriginal);
        }

        if (element instanceof HTMLInputElement &&
            ['button', 'submit', 'reset'].includes(element.type)) {
            processAttribute(element, 'value', lang, refreshOriginal);
        }
    }

    function translateTree(root, lang = getLanguage(), refreshOriginal = false) {
        if (!root) return;

        if (root.nodeType === Node.TEXT_NODE) {
            processText(root, lang, refreshOriginal);
            return;
        }

        if (!(root instanceof Element) && root !== document) return;

        if (root instanceof Element) processElement(root, lang, refreshOriginal);

        const walker = document.createTreeWalker(
            root,
            NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT
        );

        let node;
        while ((node = walker.nextNode())) {
            if (node.nodeType === Node.TEXT_NODE) {
                processText(node, lang, refreshOriginal);
            } else {
                processElement(node, lang, refreshOriginal);
            }
        }
    }

    function injectStyles() {
        if (document.getElementById('irangate-i18n-styles')) return;

        const style = document.createElement('style');
        style.id = 'irangate-i18n-styles';
        style.textContent = `
            html[dir="rtl"] body { direction: rtl; }
            html[dir="rtl"] .main-content {
                direction: rtl;
                margin-left: 0 !important;
                margin-right: 280px !important;
            }
            html[dir="rtl"] .sidebar {
                left: auto !important;
                right: 0 !important;
                direction: rtl;
                border-right: 0 !important;
                border-left: 1px solid var(--sidebar-border, rgba(100,255,218,.2)) !important;
            }
            html[dir="rtl"] .sidebar-logo { direction: ltr; }
            html[dir="rtl"] .sidebar-user,
            html[dir="rtl"] .sidebar-menu,
            html[dir="rtl"] .menu-section-title,
            html[dir="rtl"] .menu-item,
            html[dir="rtl"] .logout-btn {
                direction: rtl;
                text-align: right;
            }
            html[dir="rtl"] .menu-item::before {
                left: auto !important;
                right: 0 !important;
            }
            html[dir="rtl"] .sidebar-toggle {
                left: auto !important;
                right: 1rem !important;
            }
            html[dir="rtl"] .sidebar.compact + .main-content,
            html[dir="rtl"] .sidebar.compact ~ .main-content {
                margin-left: 0 !important;
                margin-right: 80px !important;
            }
            html[dir="rtl"] .text-left { text-align: right !important; }
            html[dir="rtl"] .text-right { text-align: left !important; }

            html[dir="rtl"] input:not([type="url"]):not([type="number"]):not(.font-mono),
            html[dir="rtl"] select,
            html[dir="rtl"] textarea { text-align: right; }

            html[dir="rtl"] code,
            html[dir="rtl"] pre,
            html[dir="rtl"] .font-mono,
            html[dir="rtl"] input[type="url"],
            html[dir="rtl"] input[type="number"],
            html[dir="rtl"] input[id*="ip" i],
            html[dir="rtl"] input[id*="url" i],
            html[dir="rtl"] input[id*="token" i],
            html[dir="rtl"] input[id*="port" i] {
                direction: ltr !important;
                text-align: left !important;
                unicode-bidi: plaintext;
            }

            #irangate-language-toggle {
                display: inline-flex;
                align-items: center;
                justify-content: center;
                min-width: 54px;
                height: 34px;
                padding: 0 9px;
                margin-inline-start: auto;
                border: 1px solid rgba(100,255,218,.42);
                border-radius: 9px;
                background: rgba(17,34,64,.92);
                color: #e6f1ff;
                font-size: 12px;
                font-weight: 800;
                line-height: 1;
                cursor: pointer;
                white-space: nowrap;
                flex: 0 0 auto;
                box-shadow: 0 3px 14px rgba(0,0,0,.18);
            }
            #irangate-language-toggle:hover {
                border-color: rgba(100,255,218,.9);
                background: rgba(30,58,95,.98);
            }
            #irangate-language-toggle.irangate-language-floating {
                position: fixed;
                top: 16px;
                inset-inline-end: 16px;
                z-index: 9999;
                margin: 0;
            }

            @media (max-width: 1024px) {
                html[dir="rtl"] .sidebar {
                    transform: translateX(100%) !important;
                }
                html[dir="rtl"] .sidebar.open {
                    transform: translateX(0) !important;
                }
                html[dir="rtl"] .main-content {
                    margin-left: 0 !important;
                    margin-right: 0 !important;
                }
            }
        `;
        document.head.appendChild(style);
    }

    function syncDirection(lang) {
        const root = document.documentElement;
        root.lang = lang === 'fa' ? 'fa' : 'en';
        root.dir = lang === 'fa' ? 'rtl' : 'ltr';
        root.dataset.irangateLang = lang;
    }

    function syncTitle(lang) {
        if (!document.documentElement.dataset.irangateOriginalTitle) {
            document.documentElement.dataset.irangateOriginalTitle = document.title || '';
        }
        const original = document.documentElement.dataset.irangateOriginalTitle;
        document.title = lang === 'fa' ? (titleFA[original] || original) : original;
    }

    function syncControl() {
        const button = document.getElementById('irangate-language-toggle');
        if (!button) return;

        const lang = getLanguage();
        const text = lang === 'fa' ? '🌐 EN' : '🌐 فا';
        const title = lang === 'fa' ? 'English' : 'فارسی';

        if (button.textContent !== text) button.textContent = text;
        if (button.title !== title) button.title = title;
        button.setAttribute(
            'aria-label',
            lang === 'fa' ? 'تغییر زبان به انگلیسی' : 'Change language to Persian'
        );
    }

    function ensureLanguageControl() {
        if (!document.body) return null;

        let button = document.getElementById('irangate-language-toggle');
        if (!button) {
            button = document.createElement('button');
            button.id = 'irangate-language-toggle';
            button.type = 'button';
            button.addEventListener('click', event => {
                event.preventDefault();
                event.stopPropagation();
                toggleLanguage();
            });
        }

        const logo = document.querySelector('.sidebar-logo');

        if (logo) {
            if (button.parentElement !== logo) logo.appendChild(button);
            button.className = '';
        } else {
            if (button.parentElement !== document.body) document.body.appendChild(button);
            button.className = 'irangate-language-floating';
        }

        syncControl();
        return button;
    }

    function setLanguage(lang, options = {}) {
        const normalized = rememberLanguage(lang);
        syncDirection(normalized);
        syncTitle(normalized);

        if (document.body) translateTree(document.body, normalized, false);

        ensureLanguageControl();
        syncControl();

        if (options.emit !== false) {
            window.dispatchEvent(new CustomEvent('irangate:language-changed', {
                detail: { language: normalized }
            }));
        }

        return normalized;
    }

    function toggleLanguage() {
        return setLanguage(getLanguage() === 'fa' ? 'en' : 'fa');
    }

    function refresh() {
        const lang = getLanguage();
        if (document.body) translateTree(document.body, lang, true);
        ensureLanguageControl();
        syncControl();
        return lang;
    }

    function startObserver() {
        if (observer || !document.body) return;

        observer = new MutationObserver(mutations => {
            const lang = getLanguage();

            for (const mutation of mutations) {
                if (mutation.type !== 'childList') continue;

                for (const node of mutation.addedNodes) {
                    if (node.nodeType !== Node.ELEMENT_NODE &&
                        node.nodeType !== Node.TEXT_NODE) continue;

                    const element = node.nodeType === Node.ELEMENT_NODE
                        ? node
                        : node.parentElement;

                    if (element && element.closest &&
                        element.closest('#irangate-language-toggle')) {
                        continue;
                    }

                    translateTree(node, lang, true);

                    if (node.nodeType === Node.ELEMENT_NODE &&
                        (node.matches?.('.sidebar, .sidebar-logo') ||
                         node.querySelector?.('.sidebar-logo'))) {
                        ensureLanguageControl();
                    }
                }
            }

            syncControl();
        });

        // Only child additions/removals are observed. i18n changes text-node
        // values and attributes, so its own translations do not retrigger this.
        observer.observe(document.body, {
            subtree: true,
            childList: true,
            characterData: false,
            attributes: false
        });
    }

    window.IranGateI18n = Object.freeze({
        getLanguage,
        setLanguage,
        toggleLanguage,
        refresh,
        ensureControl: ensureLanguageControl,
        translateTree,
        t(value) {
            return getLanguage() === 'fa' ? translateValue(value) : value;
        }
    });

    injectStyles();
    syncDirection(getLanguage());
    syncTitle(getLanguage());

    function boot() {
        setLanguage(getLanguage(), { emit: false });
        startObserver();

        for (const delay of [300, 1200, 3500]) {
            window.setTimeout(refresh, delay);
        }
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', boot, { once: true });
    } else {
        boot();
    }
})();
