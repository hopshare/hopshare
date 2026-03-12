# TODO

## Running local Postgres
    podman run --detach \
    --name postgres \
    -e POSTGRES_USER=hopuser \
    -e POSTGRES_PASSWORD=hoppass \
    -e POSTGRES_DB=hopshare \
    -e POSTGRES_ADMIN_PASSWORD=adminpass \
    -v postgres_data:/var/lib/postgresql/data:Z \
    -p 5432:5432 \
    docker.io/library/postgres:17.7


## Bugs

* I can create Hops with a due date in the past- they are then automatically expired!
* It is possible to get 'orphaned' offers to help. If the requesting User deletes your Offer message and doesn't respond, then you never get an answer...is that a problem?
* Race condition when multiple users sign up at the same time with the same First and Last name. The first one in will win as username must be unique. There is some code in here to detect unique constraint violation but it's not working.
* Move to a static tailwind CSS- don't pull dynamically


## Now

* Incorporate new UI Elements - images/colors
    * Adjust base template for graphic sidebar
    * Test on mobile device

* Set up incoming email accounts on hopshare.org

* /healthz endpoint should actually determine health- currently just returns 200

* Login
    * Add one-time login code via e-mail instead of password

* Header

* My Profile
    * If the User is not primary owner of their own Organization, give them a button at bottom of "Organizations" tab that lets them create their own Organization. 
    * Remove "Preferred Contact" field- and database
 
* Hop Detail Page

* My HopShare Dashboard

* Admin Page
    * Users
    * Organizations
        * We should let Admins 'inject' new Owners into an Organization if they get abandoned
        * The Organization name in the detail pane should be clickable to take you directly to the Organization page
 
* Joining an Organization 
    * Users can individually request to join an organization
        * We should use messages for this
        * All Owners of the organization get a message with the request- they can approve or deny.
            * The User asking to join gets a message letting them know if they've been approved or not.
        * The User asking to join gets a message telling them their request has been sent.


* Owners are moderators for listings- they can flag/delete inappropriate requests/comments

* Need an Organization-public Member page with more details about each member. Maybe have a way to send them a message?


## Later

* Hop images should not be stored in the database- instead use reliable external storage. We should use a filesystem instead. Not needed right now.

* Add in basic monitoring (cron job calling script saving in sqlite):
    * net/http/pprof package (visualize performance)
    * runtime.MemStats / runtime.ReadMemStats() thru a /health endpoint on each golang process
    * select count(*) from pg_stat_activity; (database connections)
    * iostat to see iops levels
    * jq against Caddy logs for traffic levels

* How can we use LLMs here? OpenClaw / Telegram idea from Sukumar


Font Awesome- https://icon-sets.iconify.design/fa7-regular/page-2.html?keyword=font

PgAdmin - https://www.pgadmin.org


